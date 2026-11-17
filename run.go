package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"

	"unified/internal/config"
	"unified/internal/engine"
	"unified/internal/importer"
	"unified/internal/release"
	"unified/internal/store"
)

const (
	// A release that cannot produce metadata, or makes no progress, for this
	// long is blocklisted and replaced with the next best one.
	metadataTimeout = 3 * time.Minute
	stallTimeout    = 20 * time.Minute
	// Releases to try per item before giving up for this round.
	maxAttempts = 5
	// When running continuously, wanted items with no release yet (and
	// episodes that have aired since) are searched again this often.
	retryInterval = 30 * time.Minute
	logLines      = 100
)

type activeDownload struct {
	dl       store.Download
	mediaID  int64
	t        *torrent.Torrent
	added    time.Time
	started  bool
	lastDone int64
	progress time.Time
	// For the download rate shown in the UI.
	rateDone int64
	rateAt   time.Time
	rate     float64
}

// Progress is a snapshot of one active download, for the web UI.
type Progress struct {
	ID      int64   `json:"id"`
	MediaID int64   `json:"mediaId"`
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	HasInfo bool    `json:"hasInfo"` // false while fetching metadata
	Percent float64 `json:"percent"`
	Done    int64   `json:"done"`
	Total   int64   `json:"total"`
	Peers   int     `json:"peers"`
	Rate    float64 `json:"rate"` // bytes per second
	// For seeding downloads: bytes uploaded since unified started, and that
	// as a multiple of the size.
	Uploaded int64   `json:"uploaded"`
	Ratio    float64 `json:"ratio"`
}

// runner drives items through wanted -> downloading -> imported. Everything
// except the snapshot and the request channels belongs to the loop goroutine.
type runner struct {
	ctx    context.Context
	cfg    func() *config.Config // the current config; it can change while running
	st     *store.Store
	eng    *engine.Engine
	active map[int64]*activeDownload
	// Imported downloads still in the client, until they reach the seed ratio.
	seeding map[int64]*activeDownload
	// Items with no acceptable release this round; not searched again until
	// the next retry.
	exhausted map[int64]bool
	// Print a progress line per download every tick (terminal mode).
	printProgress bool

	wake  chan struct{}
	fails chan int64
	grabs chan grabRequest

	mu       sync.Mutex
	snapshot []Progress
	seeds    []Progress
	lines    []string
	vpn      string // interface and addresses, "" when not bound
	vpnErr   string
}

// Status is what the web UI shows about the runner.
type Status struct {
	Active  []Progress `json:"active"`
	Seeding []Progress `json:"seeding"`
	Log     []string   `json:"log"` // newest first
	VPN     string     `json:"vpn"`
	VPNErr  string     `json:"vpnError"`
}

func newRunner(ctx context.Context, cfg func() *config.Config, st *store.Store) (*runner, error) {
	c := cfg()
	eng, err := engine.New(c.DownloadDir, filepath.Join(c.DataDir, "torrents"), c.ListenPort, c.VPNInterface)
	if err != nil {
		return nil, err
	}
	r := &runner{
		ctx: ctx, cfg: cfg, st: st, eng: eng,
		active:    map[int64]*activeDownload{},
		seeding:   map[int64]*activeDownload{},
		exhausted: map[int64]bool{},
		wake:      make(chan struct{}, 1),
		fails:     make(chan int64, 16),
		grabs:     make(chan grabRequest),
	}
	if vpn, _ := eng.VPN(); vpn != "" {
		r.vpn = vpn
		r.logf("torrent traffic is bound to %s", vpn)
	}
	return r, nil
}

func (r *runner) Close() { r.eng.Close() }

func cmdRun(ctx context.Context, cfg *config.Config, st *store.Store, seed bool) error {
	r, err := newRunner(ctx, func() *config.Config { return cfg }, st)
	if err != nil {
		return err
	}
	defer r.Close()
	r.printProgress = true
	if seed {
		r.logf("running until Ctrl-C; finished downloads keep seeding")
	}
	return r.loop(seed)
}

// Wake asks the loop to search for wanted items now.
func (r *runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Fail blocklists a download, stopping its torrent if it is running, and
// lets the loop look for another release.
func (r *runner) Fail(id int64) {
	select {
	case r.fails <- id:
	case <-r.ctx.Done():
	}
}

type grabRequest struct {
	items       []int64
	title, link string
	reply       chan error
}

// Grab starts a release chosen by hand for the given items, whatever its
// ranking.
func (r *runner) Grab(ctx context.Context, items []int64, title, link string) error {
	g := grabRequest{items, title, link, make(chan error, 1)}
	select {
	case r.grabs <- g:
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
	select {
	case err := <-g.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *runner) Snapshot() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	lines := make([]string, len(r.lines))
	for i, l := range r.lines {
		lines[len(lines)-1-i] = l
	}
	return Status{
		Active: append([]Progress(nil), r.snapshot...), Seeding: append([]Progress(nil), r.seeds...),
		Log: lines, VPN: r.vpn, VPNErr: r.vpnErr,
	}
}

// checkVPN updates the VPN status, logging changes, and reports whether
// torrents can run. While the VPN is down they are paused: nothing new is
// grabbed and the dead/stalled timers are held.
func (r *runner) checkVPN() bool {
	_, err := r.eng.VPN()
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	r.mu.Lock()
	changed := msg != r.vpnErr
	r.vpnErr = msg
	r.mu.Unlock()
	if changed && err != nil {
		r.logf("VPN down, torrents paused: %v", err)
	} else if changed {
		r.logf("VPN is back up, torrents resumed")
	}
	if err != nil {
		now := time.Now()
		for _, a := range r.active {
			a.added, a.progress = now, now
		}
	}
	return err == nil
}

func (r *runner) logf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	fmt.Println(line)
	r.mu.Lock()
	r.lines = append(r.lines, time.Now().Format("15:04  ")+strings.TrimSpace(line))
	if len(r.lines) > logLines {
		r.lines = r.lines[len(r.lines)-logLines:]
	}
	r.mu.Unlock()
}

// loop runs until ctx is cancelled, or with keepRunning false, until nothing
// is left to download.
func (r *runner) loop(keepRunning bool) error {
	if err := r.resume(); err != nil {
		return err
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	retry := time.NewTicker(retryInterval)
	defer retry.Stop()
	pending := true // wanted items may need a search
	for {
		up := r.checkVPN()
		if pending && up {
			if err := r.grabWanted(); err != nil {
				return err
			}
			pending = false
		}
		r.publish()
		if len(r.active) == 0 && !keepRunning {
			r.logf("nothing left to download")
			return nil
		}
		select {
		case <-r.ctx.Done():
			r.logf("stopped; unfinished downloads resume on the next start")
			return nil
		case <-r.wake:
			clear(r.exhausted)
			pending = true
			continue
		case <-retry.C:
			clear(r.exhausted)
			pending = true
			continue
		case id := <-r.fails:
			if a, ok := r.active[id]; ok {
				delete(r.active, id)
				if err := r.fail(a, "marked as failed"); err != nil {
					return err
				}
			} else if err := r.st.FailDownload(id); err != nil {
				return err
			}
			pending = true
			continue
		case g := <-r.grabs:
			if !up {
				g.reply <- errors.New("the VPN is down, torrents are paused")
				continue
			}
			dl, err := r.start(g.items, g.title, g.link)
			if err == nil {
				r.logf("grabbed [%d] %s (chosen by hand)", dl.ID, dl.Name)
			}
			g.reply <- err
			continue
		case <-tick.C:
		}
		if !up {
			continue
		}
		for id, a := range r.active {
			changed, err := r.step(a)
			if err != nil {
				return err
			}
			if changed {
				delete(r.active, id)
				pending = true
			}
		}
		r.stopSeeding()
	}
}

// stopSeeding removes imported downloads that reached the seed ratio from
// the client. Their files stay where they are.
func (r *runner) stopSeeding() {
	limit := r.cfg().SeedRatio
	if limit <= 0 {
		return
	}
	for id, a := range r.seeding {
		if ratio := seedRatio(a.t); ratio >= limit {
			r.logf("[%d] reached seed ratio %.2f, stopped seeding", id, ratio)
			a.t.Drop()
			delete(r.seeding, id)
		}
	}
}

// seedRatio is the data uploaded since the torrent was added, as a multiple
// of its size.
func seedRatio(t *torrent.Torrent) float64 {
	if !engine.HasInfo(t) || t.Length() == 0 {
		return 0
	}
	st := t.Stats()
	return float64(st.BytesWrittenData.Int64()) / float64(t.Length())
}

func (r *runner) publish() {
	var ps []Progress
	now := time.Now()
	for _, a := range r.active {
		p := Progress{
			ID: a.dl.ID, MediaID: a.mediaID, Name: a.dl.Name,
			Status: engine.Status(a.t), Peers: a.t.Stats().ActivePeers,
		}
		if engine.HasInfo(a.t) && a.t.Length() > 0 {
			p.HasInfo = true
			p.Done, p.Total = a.t.BytesCompleted(), a.t.Length()
			p.Percent = float64(p.Done) / float64(p.Total) * 100
			if el := now.Sub(a.rateAt).Seconds(); el >= 2 {
				if !a.rateAt.IsZero() {
					a.rate = float64(p.Done-a.rateDone) / el
				}
				a.rateDone, a.rateAt = p.Done, now
			}
			p.Rate = a.rate
		}
		ps = append(ps, p)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })

	var seeds []Progress
	for _, a := range r.seeding {
		st := a.t.Stats()
		seeds = append(seeds, Progress{
			ID: a.dl.ID, MediaID: a.mediaID, Name: a.dl.Name, Status: engine.Status(a.t),
			HasInfo: true, Percent: 100, Done: a.t.Length(), Total: a.t.Length(), Peers: st.ActivePeers,
			Uploaded: st.BytesWrittenData.Int64(), Ratio: seedRatio(a.t),
		})
	}
	sort.Slice(seeds, func(i, j int) bool { return seeds[i].ID < seeds[j].ID })

	r.mu.Lock()
	r.snapshot, r.seeds = ps, seeds
	r.mu.Unlock()
}

// resume re-adds downloads that were in progress when unified last stopped.
func (r *runner) resume() error {
	dls, err := r.st.Downloads(true)
	if err != nil {
		return err
	}
	for _, dl := range dls {
		t, err := r.eng.Add(dl.Source)
		if err != nil {
			r.logf("cannot resume %s: %v", dl.Name, err)
			if err := r.st.FailDownload(dl.ID); err != nil {
				return err
			}
			continue
		}
		r.logf("resuming %s", dl.Name)
		r.track(dl, t)
	}
	return nil
}

func (r *runner) track(dl store.Download, t *torrent.Torrent) {
	now := time.Now()
	a := &activeDownload{dl: dl, t: t, added: now, progress: now}
	if items, err := r.st.DownloadItems(dl.ID); err == nil && len(items) > 0 {
		a.mediaID = items[0].MediaID
	}
	r.active[dl.ID] = a
	r.publish()
}

// grabWanted searches for every wanted item. A season that is wanted in full
// is tried as a season pack first, then episode by episode.
func (r *runner) grabWanted() error {
	wanted, err := r.st.WantedItems()
	if err != nil {
		return err
	}
	if len(wanted) > 0 && len(r.cfg().Indexers) == 0 {
		r.logf("%d items wanted, but no indexers are configured", len(wanted))
		return nil
	}
	type seasonKey struct {
		media  int64
		season int
	}
	groups := map[seasonKey][]store.Item{}
	var order []seasonKey
	for _, it := range wanted {
		if r.exhausted[it.ID] {
			continue
		}
		k := seasonKey{it.MediaID, it.Season}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], it)
	}

	for _, k := range order {
		items := groups[k]
		m, err := r.st.Media(k.media)
		if err != nil {
			return err
		}
		if !m.IsShow() {
			w, _ := wantFor(r.st, m, 0, 0)
			if !r.grab(items, w) {
				r.exhausted[items[0].ID] = true
			}
			continue
		}
		pack, err := wantFor(r.st, m, k.season, 0)
		if err != nil {
			return err
		}
		if len(items) > 1 && len(items) == pack.Count && r.grab(items, pack) {
			continue
		}
		for _, it := range items {
			w, _ := wantFor(r.st, m, it.Season, it.Episode)
			if !r.grab([]store.Item{it}, w) {
				r.exhausted[it.ID] = true
			}
		}
	}
	return nil
}

// grab searches for w and starts the best release that can be added.
func (r *runner) grab(items []store.Item, w release.Want) bool {
	if r.ctx.Err() != nil {
		return false
	}
	r.logf("searching: %s", w.Query())
	cands := release.Rank(r.cfg().Quality, w, searchAll(r.ctx, r.cfg(), w, r.logf), r.st.Blocked)
	time.Sleep(time.Second) // go easy on the indexers

	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	attempts := 0
	for _, c := range cands {
		if c.Reject != "" || attempts == maxAttempts {
			break
		}
		attempts++
		dl, err := r.start(ids, c.Title, c.Link)
		if err != nil {
			r.logf("  skipping %s: %v", c.Title, err)
			continue
		}
		r.logf("  grabbed [%d] %s (%s, %d seeders)", dl.ID, c.Title, engine.Size(c.Size), c.Seeders)
		return true
	}
	r.logf("  no acceptable release found")
	return false
}

// start adds a release to the client and records it as the download for the
// given items.
func (r *runner) start(ids []int64, title, link string) (store.Download, error) {
	source, err := r.eng.Resolve(r.ctx, link)
	if err != nil {
		return store.Download{}, err
	}
	t, err := r.eng.Add(source)
	if err != nil {
		return store.Download{}, err
	}
	hash := t.InfoHash().HexString()
	if r.st.HasInfoHash(hash) {
		// Same torrent as an earlier download, maybe under another name. Only
		// drop it if it is not the one currently running or seeding.
		if !r.inClient(hash) {
			t.Drop()
		}
		return store.Download{}, errors.New("this torrent was grabbed before")
	}
	dl, err := r.st.CreateDownload(hash, title, source, ids)
	if err != nil {
		t.Drop()
		return store.Download{}, err
	}
	r.track(dl, t)
	return dl, nil
}

func (r *runner) inClient(hash string) bool {
	for _, set := range []map[int64]*activeDownload{r.active, r.seeding} {
		for _, a := range set {
			if a.dl.InfoHash == hash {
				return true
			}
		}
	}
	return false
}

// step advances one download and reports whether it left the active set.
func (r *runner) step(a *activeDownload) (finished bool, err error) {
	if r.printProgress {
		fmt.Printf("[%d] %-50s %s\n", a.dl.ID, clip(a.dl.Name, 50), engine.Status(a.t))
	}
	if !engine.HasInfo(a.t) {
		if time.Since(a.added) > metadataTimeout {
			return true, r.fail(a, "no metadata from peers")
		}
		return false, nil
	}
	if !a.started {
		a.t.DownloadAll()
		a.started = true
	}
	if done := a.t.BytesCompleted(); done != a.lastDone {
		a.lastDone, a.progress = done, time.Now()
	}
	if a.t.Complete().Bool() {
		return true, r.finish(a)
	}
	if time.Since(a.progress) > stallTimeout {
		return true, r.fail(a, "stalled")
	}
	return false, nil
}

func (r *runner) fail(a *activeDownload, why string) error {
	r.logf("[%d] failed (%s), blocklisting and looking for another release", a.dl.ID, why)
	a.t.Drop()
	return r.st.FailDownload(a.dl.ID)
}

// finish imports a completed download. The torrent stays in the client so it
// keeps seeding until it reaches the seed ratio, or unified stops.
func (r *runner) finish(a *activeDownload) error {
	items, err := r.st.DownloadItems(a.dl.ID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return r.done(a)
	}
	m, err := r.st.Media(items[0].MediaID)
	if err != nil {
		return err
	}
	var files []importer.File
	for _, f := range a.t.Files() {
		files = append(files, importer.File{Path: f.Path(), Size: f.Length()})
	}
	results, err := importer.Import(r.cfg(), m, items, a.t.Name(), files)
	for _, res := range results {
		if err := r.st.SetImported(res.ItemID, res.Dest); err != nil {
			return err
		}
		r.logf("[%d] imported %s", a.dl.ID, res.Dest)
	}
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return r.fail(a, "no file matched what was wanted")
	}
	return r.done(a)
}

func (r *runner) done(a *activeDownload) error {
	if err := r.st.FinishDownload(a.dl.ID); err != nil {
		return err
	}
	r.seeding[a.dl.ID] = a
	return nil
}

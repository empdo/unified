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
	"unified/internal/disk"
	"unified/internal/engine"
	"unified/internal/importer"
	"unified/internal/notify"
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

	wake    chan struct{}
	fails   chan int64
	grabs   chan grabRequest
	removes chan removeRequest

	mu       sync.Mutex
	snapshot []Progress
	seeds    []Progress
	lines    []string
	vpn      string // interface and addresses, "" when not bound
	vpnErr   string
	disk     DiskStatus
}

// Status is what the web UI shows about the runner.
type Status struct {
	Active  []Progress `json:"active"`
	Seeding []Progress `json:"seeding"`
	Log     []string   `json:"log"` // newest first
	VPN     string     `json:"vpn"`
	VPNErr  string     `json:"vpnError"`
	Disk    DiskStatus `json:"disk"`
}

// DiskStatus is the free space for downloads and the library.
type DiskStatus struct {
	Volumes []disk.Volume `json:"volumes"`
	// Free space for downloads once the active ones have finished.
	Available uint64 `json:"available"`
	// The space kept free; 0 when not checked.
	MinFree uint64 `json:"minFree"`
	// Low is set while new downloads are held back for lack of space.
	Low   bool   `json:"low"`
	Error string `json:"error,omitempty"`
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
		removes:   make(chan removeRequest),
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

type removeRequest struct {
	media                    int64
	deleteFiles, fromLibrary bool
	reply                    chan error
}

// Remove stops the downloads of a movie or show. With deleteFiles it also
// deletes its files, from the library and the download directory, and stops
// wanting it; with fromLibrary it is removed from the library.
func (r *runner) Remove(ctx context.Context, media int64, deleteFiles, fromLibrary bool) error {
	req := removeRequest{media, deleteFiles, fromLibrary, make(chan error, 1)}
	select {
	case r.removes <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
	select {
	case err := <-req.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *runner) remove(req removeRequest) error {
	var downloaded []string
	stop := func(a *activeDownload) {
		if req.deleteFiles && engine.HasInfo(a.t) {
			for _, f := range a.t.Files() {
				downloaded = append(downloaded, filepath.Join(r.cfg().DownloadDir, f.Path()))
			}
		}
		a.t.Drop()
	}
	for id, a := range r.active {
		if a.mediaID == req.media {
			stop(a)
			delete(r.active, id)
			if err := r.st.CancelDownload(id); err != nil {
				return err
			}
		}
	}
	for id, a := range r.seeding {
		if a.mediaID == req.media {
			stop(a)
			delete(r.seeding, id)
		}
	}
	defer r.publish()

	m, err := r.st.Media(req.media)
	if err != nil {
		return err
	}
	if req.deleteFiles {
		items, err := r.st.Items(m.ID)
		if err != nil {
			return err
		}
		var library []string
		for _, it := range items {
			if it.Path != "" {
				library = append(library, it.Path)
			}
		}
		// Whatever could not be deleted, nothing should be downloaded again.
		deleteErr := importer.Delete(r.cfg(), library, downloaded)
		if err := r.st.ClearItems(m.ID); err != nil {
			return err
		}
		if deleteErr != nil {
			r.logf("deleting the files of %s: %v", m.Title, deleteErr)
			return deleteErr
		}
		r.logf("deleted the files of %s", m.Title)
	}
	if req.fromLibrary {
		r.logf("removed %s from the library", m.Title)
		return r.st.RemoveMedia(m.ID)
	}
	return nil
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
		Log: lines, VPN: r.vpn, VPNErr: r.vpnErr, Disk: r.disk,
	}
}

// available is the free space for downloads, less what the active downloads
// still need.
func (r *runner) available() (uint64, error) {
	free, err := disk.Free(r.cfg().DownloadDir)
	if err != nil {
		return 0, err
	}
	for _, a := range r.active {
		if engine.HasInfo(a.t) {
			free -= min(free, uint64(max(a.t.Length()-a.t.BytesCompleted(), 0)))
		}
	}
	return free, nil
}

// minFree is the space to keep free for downloads, 0 when not checked.
func (r *runner) minFree() uint64 { return uint64(r.cfg().MinFreeGB * (1 << 30)) }

// checkDisk updates the disk status, logging and notifying when space runs
// low or recovers, and reports whether new downloads may start.
func (r *runner) checkDisk() bool {
	c := r.cfg()
	d := DiskStatus{
		Volumes: disk.Volumes([]disk.Dir{
			{Label: "Downloads", Path: c.DownloadDir},
			{Label: "Movies", Path: c.MoviesDir},
			{Label: "TV", Path: c.TVDir},
		}),
		MinFree: r.minFree(),
	}
	avail, err := r.available()
	if err != nil {
		d.Error = err.Error()
	}
	d.Available = avail
	d.Low = err == nil && d.MinFree > 0 && avail < d.MinFree

	r.mu.Lock()
	changed := d.Low != r.disk.Low
	r.disk = d
	r.mu.Unlock()
	if changed && d.Low {
		msg := fmt.Sprintf("Only %s free for downloads, less than the %s to keep free. Nothing new is downloaded until there is more space.",
			engine.Size(int64(avail)), engine.Size(int64(d.MinFree)))
		r.logf("%s", msg)
		r.notify("Low on disk space", msg)
	} else if changed {
		r.logf("enough free space again (%s), downloads resume", engine.Size(int64(avail)))
	}
	return !d.Low
}

// notify sends a notification, if they are set up, without holding up the
// caller.
func (r *runner) notify(title, message string) {
	u := r.cfg().NotifyURL
	if u == "" {
		return
	}
	go func() {
		if err := notify.Send(r.ctx, u, title, message); err != nil && r.ctx.Err() == nil {
			r.logf("could not send a notification: %v", err)
		}
	}()
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
		r.notify("VPN down", fmt.Sprintf("Torrents are paused until it is back: %v", err))
	} else if changed {
		r.logf("VPN is back up, torrents resumed")
		r.notify("VPN back up", "Torrents resumed.")
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
	r.refreshScenes()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	retry := time.NewTicker(retryInterval)
	defer retry.Stop()
	pending := true // wanted items may need a search
	for {
		up := r.checkVPN()
		r.checkDisk()
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
		case req := <-r.removes:
			req.reply <- r.remove(req)
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

// refreshScenes updates the scene numbering of every show, which changes as
// new seasons air and TheXEM's mappings are filled in.
func (r *runner) refreshScenes() {
	if r.cfg().TMDBKey == "" {
		return
	}
	media, err := r.st.AllMedia()
	if err != nil {
		r.logf("could not list the library: %v", err)
		return
	}
	for _, m := range media {
		if !m.IsShow() || r.ctx.Err() != nil {
			continue
		}
		if err := refreshScene(r.cfg(), r.st, m); err != nil {
			r.logf("could not look up scene numbering for %s: %v", m.Title, err)
		}
	}
}

// grabWanted searches for every wanted item. A scene season that is wanted
// in full is tried as a season pack first, then episode by episode.
func (r *runner) grabWanted() error {
	wanted, err := r.st.WantedItems()
	if err != nil {
		return err
	}
	if len(wanted) > 0 && len(r.cfg().Indexers) == 0 {
		r.logf("%d items wanted, but no indexers are configured", len(wanted))
		return nil
	}
	if len(wanted) > 0 && !r.checkDisk() {
		r.logf("%d items wanted, but disk space is low; not searching", len(wanted))
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
		season, _ := it.Scene()
		k := seasonKey{it.MediaID, season}
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
			if !r.grab(items, sceneWant(m, nil, 0, 0)) {
				r.exhausted[items[0].ID] = true
			}
			continue
		}
		all, err := r.st.Items(m.ID)
		if err != nil {
			return err
		}
		pack := sceneWant(m, all, k.season, 0)
		if len(items) > 1 && len(items) == pack.Count && r.grab(items, pack) {
			continue
		}
		for _, it := range items {
			season, episode := it.Scene()
			if !r.grab([]store.Item{it}, sceneWant(m, all, season, episode)) {
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
	attempts, tooBig := 0, 0
	for _, c := range cands {
		if c.Reject != "" || attempts == maxAttempts {
			break
		}
		if !r.fits(c.Size) {
			tooBig++
			continue
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
	if tooBig > 0 {
		r.logf("  %d acceptable releases would leave less than %s free", tooBig, engine.Size(int64(r.minFree())))
	} else {
		r.logf("  no acceptable release found")
	}
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

// fits reports whether a download of size bytes leaves the space to keep
// free. Unknown sizes and space are given the benefit of the doubt.
func (r *runner) fits(size int64) bool {
	minFree := r.minFree()
	if minFree == 0 || size <= 0 {
		return true
	}
	avail, err := r.available()
	return err != nil || avail >= minFree && avail-minFree >= uint64(size)
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
	r.notify("Download failed", fmt.Sprintf("%s failed (%s). Looking for another release.", a.dl.Name, why))
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
	var imported []store.Item
	for _, res := range results {
		if err := r.st.SetImported(res.ItemID, res.Dest); err != nil {
			return err
		}
		r.logf("[%d] imported %s", a.dl.ID, res.Dest)
		for _, it := range items {
			if it.ID == res.ItemID {
				imported = append(imported, it)
			}
		}
	}
	if len(imported) > 0 {
		r.notify("Imported", describe(m, imported)+" is in your library.")
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

// describe names a movie, or some episodes of a show: "Severance S02E03 -
// Who Is Alive?", "Severance season 2 (10 episodes)".
func describe(m store.Media, items []store.Item) string {
	title := m.Title
	if m.Year > 0 {
		title = fmt.Sprintf("%s (%d)", m.Title, m.Year)
	}
	if !m.IsShow() || len(items) == 0 {
		return title
	}
	if len(items) == 1 {
		it := items[0]
		s := fmt.Sprintf("%s S%02dE%02d", m.Title, it.Season, it.Episode)
		if it.Title != "" {
			s += " - " + it.Title
		}
		return s
	}
	for _, it := range items[1:] {
		if it.Season != items[0].Season {
			return fmt.Sprintf("%s (%d episodes)", m.Title, len(items))
		}
	}
	return fmt.Sprintf("%s season %d (%d episodes)", m.Title, items[0].Season, len(items))
}

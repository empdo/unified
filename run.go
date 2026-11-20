package main

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
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
	// A download that has nothing yet is given up on sooner when no
	// connected peer has any of it either: no seeder is reachable, and
	// waiting rarely changes that.
	noSeederTimeout = 5 * time.Minute
	// Releases to try per item before giving up for this round.
	maxAttempts = 5
	// When running continuously, wanted items with no release yet (and
	// episodes that have aired since) are searched again this often.
	retryInterval = 30 * time.Minute
	logLines      = 100
	// How often the bytes downloaded and uploaded are saved, so a crash
	// loses little of the totals and seed ratios.
	saveInterval = time.Minute
)

type activeDownload struct {
	dl       store.Download
	mediaID  int64
	t        *torrent.Torrent
	added    time.Time
	started  bool
	lastDone int64
	progress time.Time
	// When a connected peer last had any of the torrent.
	seedSeen time.Time
	// For the download rate shown in the UI.
	rateDone int64
	rateAt   time.Time
	rate     float64
	// The connection limit last set, -1 before the first.
	maxConns int
	// For the upload rate shown in the UI.
	upDone int64
	upAt   time.Time
	upRate float64
	// For seeding: since when, and whether it waits for a seeding slot.
	seedSince time.Time
	paused    bool
	// Bytes downloaded and uploaded in earlier runs, before the torrent was
	// added this time; the torrent's own counts start from 0.
	downBefore, upBefore int64
	// A seed added back after a restart, whose files are yet to be checked.
	resumed bool
}

// transferred is the torrent data the download has downloaded and uploaded,
// over all runs.
func (a *activeDownload) transferred() (down, up int64) {
	st := a.t.Stats()
	return a.downBefore + st.BytesReadUsefulData.Int64(), a.upBefore + st.BytesWrittenData.Int64()
}

// ratio is the data uploaded, over all runs, as a multiple of its size.
func (a *activeDownload) ratio() float64 {
	if !engine.HasInfo(a.t) || a.t.Length() == 0 {
		return 0
	}
	_, up := a.transferred()
	return float64(up) / float64(a.t.Length())
}

// measureUpload returns the upload rate, in bytes per second, updating it
// every couple of seconds.
func (a *activeDownload) measureUpload(now time.Time) float64 {
	st := a.t.Stats()
	up := st.BytesWrittenData.Int64()
	if el := now.Sub(a.upAt).Seconds(); el >= 2 {
		if !a.upAt.IsZero() {
			a.upRate = float64(up-a.upDone) / el
		}
		a.upDone, a.upAt = up, now
	}
	return a.upRate
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
	UpRate  float64 `json:"upRate"`
	// Bytes uploaded over all runs, and that as a multiple of the size.
	Uploaded int64   `json:"uploaded"`
	Ratio    float64 `json:"ratio"`
	// Peer connections over uTP and TCP.
	UTP int `json:"utp"`
	TCP int `json:"tcp"`
	// Connected peers with the whole torrent, peers known of, and the
	// largest share of the torrent (0 to 1) any connected peer has.
	Seeders int     `json:"seeders"`
	Known   int     `json:"known"`
	Best    float64 `json:"best"`
	// When it started, to tell a slow start from a stuck one, and when it
	// is given up on unless it makes progress.
	Started   time.Time `json:"started,omitzero"`
	GivesUpAt time.Time `json:"givesUpAt,omitzero"`
	// Waiting for a slot: a queued download's place in line (from 1), or a
	// seed that waits to seed.
	Position int  `json:"position,omitempty"`
	Paused   bool `json:"paused,omitempty"`
}

// runner drives items through wanted -> downloading -> imported. Everything
// except the snapshot and the request channels belongs to the loop goroutine.
type runner struct {
	ctx    context.Context
	cfg    func() *config.Config // the current config; it can change while running
	st     *store.Store
	eng    *engine.Engine
	active map[int64]*activeDownload
	// Downloads waiting for a slot in active, oldest first.
	queue []queuedDownload
	// Imported downloads still in the client, until they reach the seed ratio.
	seeding map[int64]*activeDownload
	// Items with no acceptable release this round; not searched again until
	// the next retry.
	exhausted map[int64]bool
	// Print a progress line per download every tick (terminal mode).
	printProgress bool
	// Indexers that failed recently are skipped for a while.
	indexers indexerHealth

	wake    chan struct{}
	fails   chan int64
	grabs   chan grabRequest
	removes chan removeRequest

	mu        sync.Mutex
	snapshot  []Progress
	queued    []Progress
	seeds     []Progress
	limits    LimitStatus
	limitsSet bool
	lines     []string
	vpn       string // interface and addresses, "" when not bound
	vpnErr    string
	disk      DiskStatus
	// Where the search for each wanted item stands, by item, and what is
	// being searched for right now.
	searches  map[int64]ItemSearch
	searching *SearchView
	transfer  TransferStatus

	// The engine's totals when last measured and last saved, and the totals
	// of earlier runs. Only the loop goroutine uses these.
	measured, saved     [2]int64
	measuredAt, savedAt time.Time
	before              [2]int64
}

// TransferStatus is how much torrent data has been moved, and how fast it
// moves now.
type TransferStatus struct {
	// Over all runs, and since unified started.
	Downloaded        int64 `json:"downloaded"`
	Uploaded          int64 `json:"uploaded"`
	SessionDownloaded int64 `json:"sessionDownloaded"`
	SessionUploaded   int64 `json:"sessionUploaded"`
	// Bytes per second, over all torrents.
	DownRate float64 `json:"downRate"`
	UpRate   float64 `json:"upRate"`
}

// ItemSearch is where the search for a wanted item stands.
type ItemSearch struct {
	State string    `json:"state"` // queued, searching or notFound
	At    time.Time `json:"at"`
	Note  string    `json:"note,omitempty"` // why nothing was grabbed
}

// SearchView is a search in progress, for the web UI.
type SearchView struct {
	MediaID int64  `json:"mediaId"`
	Label   string `json:"label"` // "Severance S02E03"
}

// SearchStatus is what is being searched for, and how many items wait.
type SearchStatus struct {
	Current *SearchView `json:"current"`
	Queued  int         `json:"queued"`
}

// Status is what the web UI shows about the runner.
type Status struct {
	Active   []Progress     `json:"active"`
	Seeding  []Progress     `json:"seeding"`
	Log      []string       `json:"log"` // newest first
	VPN      string         `json:"vpn"`
	VPNErr   string         `json:"vpnError"`
	Disk     DiskStatus     `json:"disk"`
	Search   SearchStatus   `json:"search"`
	Queued   []Progress     `json:"queued"` // downloads waiting for a slot, in order
	Limits   LimitStatus    `json:"limits"`
	Transfer TransferStatus `json:"transfer"`
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
		searches:  map[int64]ItemSearch{},
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
	media int64
	// Only these items of it: their files are deleted and they are no
	// longer wanted. Nil means all of it.
	items                    []int64
	deleteFiles, fromLibrary bool
	// With items: only stop their downloads, keeping files in the library.
	cancel bool
	reply  chan error
}

// Remove stops the downloads of a movie or show. With deleteFiles it also
// deletes its files, from the library and the download directory, and stops
// wanting it; with fromLibrary it is removed from the library.
func (r *runner) Remove(ctx context.Context, media int64, deleteFiles, fromLibrary bool) error {
	return r.request(ctx, removeRequest{media: media, deleteFiles: deleteFiles, fromLibrary: fromLibrary})
}

// DeleteItems deletes the files of some episodes of a show and stops wanting
// them. Their downloads are stopped, unless a download still running also
// brings other episodes; then only those are imported when it finishes.
func (r *runner) DeleteItems(ctx context.Context, media int64, items []int64) error {
	if len(items) == 0 {
		return nil
	}
	return r.request(ctx, removeRequest{media: media, items: items, deleteFiles: true})
}

// CancelItems stops the downloads of some items and stops wanting them,
// deleting what was downloaded. Files in the library stay. A download that
// also brings other items keeps running for those.
func (r *runner) CancelItems(ctx context.Context, media int64, items []int64) error {
	if len(items) == 0 {
		return nil
	}
	return r.request(ctx, removeRequest{media: media, items: items, cancel: true})
}

func (r *runner) request(ctx context.Context, req removeRequest) error {
	req.reply = make(chan error, 1)
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
	if req.items != nil {
		return r.removeItems(req)
	}
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
			if err := r.st.SetSeeding(id, false); err != nil {
				return err
			}
		}
	}
	for _, q := range r.unqueue(func(q queuedDownload) bool { return q.mediaID == req.media }) {
		if err := r.st.CancelDownload(q.dl.ID); err != nil {
			return err
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
		r.record(store.EventDeleted, m.ID, describe(m, nil), fmt.Sprintf("%d files", len(library)))
	}
	if req.fromLibrary {
		r.logf("removed %s from the library", m.Title)
		r.record(store.EventRemoved, m.ID, describe(m, nil), "")
		return r.st.RemoveMedia(m.ID)
	}
	return nil
}

// removeItems deletes the files of some items of a movie or show.
func (r *runner) removeItems(req removeRequest) error {
	removing := map[int64]bool{}
	for _, id := range req.items {
		removing[id] = true
	}
	// covers reports whether a download brings any of the items, and whether
	// it brings nothing else.
	covers := func(id int64) (some, only bool, err error) {
		items, err := r.st.DownloadItems(id)
		if err != nil {
			return false, false, err
		}
		only = len(items) > 0
		for _, it := range items {
			if removing[it.ID] {
				some = true
			} else {
				only = false
			}
		}
		return some, only, nil
	}
	var downloaded []string
	files := func(a *activeDownload) {
		if engine.HasInfo(a.t) {
			for _, f := range a.t.Files() {
				downloaded = append(downloaded, filepath.Join(r.cfg().DownloadDir, f.Path()))
			}
		}
	}
	for id, a := range r.active {
		some, only, err := covers(id)
		if err != nil {
			return err
		}
		if some && only {
			files(a)
			a.t.Drop()
			delete(r.active, id)
			if err := r.st.CancelDownload(id); err != nil {
				return err
			}
		}
	}
	var coverErr error
	unqueued := r.unqueue(func(q queuedDownload) bool {
		some, only, err := covers(q.dl.ID)
		coverErr = cmp.Or(coverErr, err)
		return some && only
	})
	if coverErr != nil {
		return coverErr
	}
	for _, q := range unqueued {
		if err := r.st.CancelDownload(q.dl.ID); err != nil {
			return err
		}
	}
	// A seeding torrent loses the files of the removed items, so it stops;
	// the files of other items stay, as when it reaches the seed ratio.
	// Cancelling leaves finished downloads alone.
	for id, a := range r.seeding {
		if req.cancel {
			break
		}
		some, only, err := covers(id)
		if err != nil {
			return err
		}
		if some {
			if only {
				files(a)
			}
			a.t.Drop()
			delete(r.seeding, id)
			if err := r.st.SetSeeding(id, false); err != nil {
				return err
			}
		}
	}
	defer r.publish()

	m, err := r.st.Media(req.media)
	if err != nil {
		return err
	}
	all, err := r.st.Items(m.ID)
	if err != nil {
		return err
	}
	var items []store.Item
	var library []string
	for _, it := range all {
		if removing[it.ID] {
			items = append(items, it)
			if it.Path != "" && !req.cancel {
				library = append(library, it.Path)
			}
		}
	}
	if req.cancel {
		deleteErr := importer.Delete(r.cfg(), nil, downloaded)
		if err := r.st.CancelItems(req.items); err != nil {
			return err
		}
		if deleteErr != nil {
			r.logf("cancelling the downloads of %s: %v", describe(m, items), deleteErr)
			return deleteErr
		}
		r.logf("cancelled the downloads of %s", describe(m, items))
		r.record(store.EventCancelled, m.ID, describe(m, items), "")
		return nil
	}
	deleteErr := importer.Delete(r.cfg(), library, downloaded)
	// Whatever could not be deleted, nothing should be downloaded again.
	if err := r.st.ClearItemsByID(req.items); err != nil {
		return err
	}
	if deleteErr != nil {
		r.logf("deleting the files of %s: %v", describe(m, items), deleteErr)
		return deleteErr
	}
	r.logf("deleted the files of %s", describe(m, items))
	r.record(store.EventDeleted, m.ID, describe(m, items), fmt.Sprintf("%d files", len(library)))
	return nil
}

func (r *runner) Snapshot() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	queued := 0
	for _, s := range r.searches {
		if s.State == "queued" {
			queued++
		}
	}
	lines := make([]string, len(r.lines))
	for i, l := range r.lines {
		lines[len(lines)-1-i] = l
	}
	return Status{
		Active: append([]Progress(nil), r.snapshot...), Seeding: append([]Progress(nil), r.seeds...),
		Queued: append([]Progress(nil), r.queued...), Limits: r.limits,
		Log: lines, VPN: r.vpn, VPNErr: r.vpnErr, Disk: r.disk,
		Search: SearchStatus{Current: r.searching, Queued: queued}, Transfer: r.transfer,
	}
}

// Searches returns where the searches for wanted items stand, by item.
func (r *runner) Searches() map[int64]ItemSearch {
	r.mu.Lock()
	defer r.mu.Unlock()
	return maps.Clone(r.searches)
}

// setSearch records where the search for some items stands; an empty state
// forgets them.
func (r *runner) setSearch(items []store.Item, state, note string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, it := range items {
		if state == "" {
			delete(r.searches, it.ID)
		} else {
			r.searches[it.ID] = ItemSearch{State: state, At: time.Now(), Note: note}
		}
	}
}

// available is the free space for downloads, less what the active and
// queued downloads still need.
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
	for _, q := range r.queue {
		free -= min(free, uint64(max(q.size, 0)))
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
			a.added, a.progress, a.seedSeen = now, now, now
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
	r.before = [2]int64{r.st.Counter(store.TotalDownloaded), r.st.Counter(store.TotalUploaded)}
	r.measureTransfer()
	defer r.saveTransfers()
	if err := r.resume(keepRunning); err != nil {
		return err
	}
	r.refreshScenes()
	r.applyRateLimits()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	retry := time.NewTicker(retryInterval)
	defer retry.Stop()
	pending := true // wanted items may need a search
	for {
		up := r.checkVPN()
		r.checkDisk()
		r.applyRateLimits()
		r.measureTransfer()
		if pending && up {
			if err := r.grabWanted(); err != nil {
				return err
			}
			pending = false
		}
		r.publish()
		if len(r.active) == 0 && len(r.queue) == 0 && !keepRunning {
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
			} else {
				for _, q := range r.unqueue(func(q queuedDownload) bool { return q.dl.ID == id }) {
					r.recordDownload(store.EventFailed, q.dl.ID, q.dl.Name+": marked as failed")
				}
				if err := r.st.FailDownload(id); err != nil {
					return err
				}
			}
			pending = true
			continue
		case g := <-r.grabs:
			r.grabByHand(g, up)
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
		if err := r.fillSlots(); err != nil {
			return err
		}
		r.stopSeeding()
		r.limitSeeds()
		r.balanceConns()
	}
}

func (r *runner) grabByHand(g grabRequest, up bool) {
	if !up {
		g.reply <- errors.New("the VPN is down, torrents are paused")
		return
	}
	dl, err := r.start(g.items, g.title, g.link, 0)
	if err == nil {
		r.logf("grabbed [%d] %s (chosen by hand)", dl.ID, dl.Name)
		r.recordDownload(store.EventGrabbed, dl.ID, dl.Name+" (chosen by hand)")
	}
	g.reply <- err
}

// serveRequests handles grabs and removals asked for from the web UI, so
// they need not wait for a whole search round. It does not block.
func (r *runner) serveRequests() {
	for {
		select {
		case g := <-r.grabs:
			r.grabByHand(g, true) // a search round only runs while the VPN is up
		case req := <-r.removes:
			req.reply <- r.remove(req)
		default:
			return
		}
	}
}

// stillWanted returns the items that are still wanted, which a request
// served during a search round may have changed.
func (r *runner) stillWanted(items []store.Item) ([]store.Item, error) {
	var out []store.Item
	for _, it := range items {
		cur, err := r.st.Item(it.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue // removed from the library
		}
		if err != nil {
			return nil, err
		}
		if cur.State == store.Wanted {
			out = append(out, cur)
		} else {
			r.setSearch([]store.Item{it}, "", "")
		}
	}
	return out, nil
}

// stopSeeding removes imported downloads that reached the seed ratio from
// the client. Their files stay where they are.
// Seeds whose files are gone, as when they were deleted while unified was
// not running, are stopped too.
func (r *runner) stopSeeding() {
	limit := r.cfg().SeedRatio
	for id, a := range r.seeding {
		if a.resumed && engine.HasInfo(a.t) {
			a.resumed = false
			if missing := r.missingFile(a.t); missing != "" {
				r.logf("[%d] %s is gone, stopped seeding %s", id, missing, a.dl.Name)
				r.stopSeed(a)
				continue
			}
		}
		if ratio := a.ratio(); limit > 0 && ratio >= limit {
			r.logf("[%d] reached seed ratio %.2f, stopped seeding", id, ratio)
			_, up := a.transferred()
			r.record(store.EventSeeded, a.mediaID, r.mediaTitle(a.mediaID, a.dl.Name),
				fmt.Sprintf("%s: uploaded %s, ratio %.2f", a.dl.Name, engine.Size(up), ratio))
			r.stopSeed(a)
		}
	}
}

// stopSeed removes a seed from the client for good: it is not seeded again
// after a restart. Its files stay.
func (r *runner) stopSeed(a *activeDownload) {
	r.saveDownload(a)
	a.t.Drop()
	delete(r.seeding, a.dl.ID)
	if err := r.st.SetSeeding(a.dl.ID, false); err != nil {
		r.logf("[%d] %v", a.dl.ID, err)
	}
}

// missingFile returns the first of a torrent's files that is not in the
// download directory at its full size, or "" when all are.
func (r *runner) missingFile(t *torrent.Torrent) string {
	for _, f := range t.Files() {
		fi, err := os.Stat(filepath.Join(r.cfg().DownloadDir, f.Path()))
		if err != nil || fi.Size() < f.Length() {
			return f.Path()
		}
	}
	return ""
}

// measureTransfer updates the totals and rates of data moved, and saves
// them every saveInterval.
func (r *runner) measureTransfer() {
	if r.eng == nil {
		return
	}
	down, up := r.eng.Totals()
	now := time.Now()
	r.mu.Lock()
	t := r.transfer
	if el := now.Sub(r.measuredAt).Seconds(); !r.measuredAt.IsZero() && el > 0 {
		t.DownRate = float64(down-r.measured[0]) / el
		t.UpRate = float64(up-r.measured[1]) / el
	}
	t.SessionDownloaded, t.SessionUploaded = down, up
	t.Downloaded, t.Uploaded = r.before[0]+down, r.before[1]+up
	r.transfer = t
	r.mu.Unlock()
	r.measured, r.measuredAt = [2]int64{down, up}, now
	if r.savedAt.IsZero() {
		r.savedAt = now
	} else if now.Sub(r.savedAt) >= saveInterval {
		r.saveTransfers()
	}
}

// saveTransfers saves the totals of data moved, and what each download in
// the client has moved.
func (r *runner) saveTransfers() {
	if r.eng == nil {
		return
	}
	down, up := r.eng.Totals()
	if err := r.st.AddCounters(map[string]int64{
		store.TotalDownloaded: down - r.saved[0],
		store.TotalUploaded:   up - r.saved[1],
	}); err != nil {
		r.logf("could not save transfer totals: %v", err)
		return
	}
	r.saved, r.savedAt = [2]int64{down, up}, time.Now()
	for _, a := range r.active {
		r.saveDownload(a)
	}
	for _, a := range r.seeding {
		r.saveDownload(a)
	}
}

func (r *runner) saveDownload(a *activeDownload) {
	down, up := a.transferred()
	if err := r.st.SetTransferred(a.dl.ID, down, up); err != nil {
		r.logf("[%d] could not save what it transferred: %v", a.dl.ID, err)
	}
}

func (r *runner) publish() {
	var ps []Progress
	now := time.Now()
	for _, a := range r.active {
		p := Progress{
			ID: a.dl.ID, MediaID: a.mediaID, Name: a.dl.Name,
			Status: engine.Status(a.t), Peers: a.t.Stats().ActivePeers,
		}
		p.UTP, p.TCP, _ = engine.Conns(a.t)
		p.Seeders, p.Known, p.Best = engine.Swarm(a.t)
		p.Started = a.added
		p.UpRate = a.measureUpload(now)
		if engine.HasInfo(a.t) {
			p.GivesUpAt, _ = giveUpAt(a.progress, a.seedSeen, a.lastDone)
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

	var queued []Progress
	for i, q := range r.queue {
		queued = append(queued, Progress{
			ID: q.dl.ID, MediaID: q.mediaID, Name: q.dl.Name, Status: "queued", Total: q.size, Position: i + 1,
		})
	}

	var seeds []Progress
	for _, a := range r.seeding {
		_, up := a.transferred()
		p := Progress{
			ID: a.dl.ID, MediaID: a.mediaID, Name: a.dl.Name, Status: engine.Status(a.t),
			HasInfo: engine.HasInfo(a.t), Percent: 100, Done: a.t.Length(), Total: a.t.Length(), Peers: a.t.Stats().ActivePeers,
			Uploaded: up, Ratio: a.ratio(), Paused: a.paused, UpRate: a.measureUpload(now),
		}
		if !p.HasInfo {
			p.Done, p.Total = 0, 0
		}
		p.UTP, p.TCP, _ = engine.Conns(a.t)
		seeds = append(seeds, p)
	}
	sort.Slice(seeds, func(i, j int) bool { return seeds[i].ID < seeds[j].ID })

	r.mu.Lock()
	r.snapshot, r.queued, r.seeds = ps, queued, seeds
	r.mu.Unlock()
}

// resume puts the downloads that were unfinished when unified last stopped
// back in line, oldest first, and starts as many as there are slots for.
// With seed set, finished downloads that were still seeding seed again.
func (r *runner) resume(seed bool) error {
	dls, err := r.st.Downloads(true)
	if err != nil {
		return err
	}
	for _, dl := range dls {
		r.logf("resuming %s", dl.Name)
		r.enqueue(dl, 0)
	}
	if seed {
		if err := r.resumeSeeds(); err != nil {
			return err
		}
	}
	if !r.checkVPN() {
		return nil // they start once the VPN is up
	}
	return r.fillSlots()
}

// resumeSeeds adds the downloads that were seeding when unified last
// stopped back to the client. They keep their place in line for a seeding
// slot and their seed ratio.
func (r *runner) resumeSeeds() error {
	dls, err := r.st.SeedingDownloads()
	if err != nil {
		return err
	}
	for _, dl := range dls {
		t, err := r.eng.Add(dl.Source)
		if err != nil {
			r.logf("[%d] cannot seed %s again: %v", dl.ID, dl.Name, err)
			if err := r.st.SetSeeding(dl.ID, false); err != nil {
				return err
			}
			continue
		}
		r.seeding[dl.ID] = &activeDownload{
			dl: dl, mediaID: r.st.DownloadMedia(dl.ID), t: t, added: time.Now(), maxConns: -1,
			seedSince: cmp.Or(dl.FinishedAt, time.Now()), downBefore: dl.Downloaded, upBefore: dl.Uploaded, resumed: true,
		}
	}
	if len(dls) > 0 {
		r.logf("seeding %d finished downloads again", len(r.seeding))
	}
	r.limitSeeds()
	r.balanceConns()
	return nil
}

func (r *runner) track(dl store.Download, mediaID int64, t *torrent.Torrent) {
	now := time.Now()
	r.active[dl.ID] = &activeDownload{
		dl: dl, mediaID: mediaID, t: t, added: now, progress: now, seedSeen: now, maxConns: -1,
		downBefore: dl.Downloaded, upBefore: dl.Uploaded,
	}
	r.balanceConns()
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
		r.setSearch(wanted, "notFound", "No indexers are set up")
		return nil
	}
	if len(wanted) > 0 && !r.checkDisk() {
		r.logf("%d items wanted, but disk space is low; not searching", len(wanted))
		r.setSearch(wanted, "notFound", "Low on disk space")
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
		r.setSearch([]store.Item{it}, "queued", "")
	}
	// Whatever was not reached, as when unified stops, is not waiting.
	defer func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for id, s := range r.searches {
			if s.State == "queued" || s.State == "searching" {
				delete(r.searches, id)
			}
		}
	}()

	var names []string
	for _, ix := range r.cfg().Indexers {
		names = append(names, ix.Name)
	}
	for i, k := range order {
		r.serveRequests()
		items, err := r.stillWanted(groups[k])
		if err != nil {
			return err
		}
		if len(items) == 0 {
			continue
		}
		// With every indexer failing, the rest wait until one is back.
		if until, down := r.indexers.allDown(names); down {
			var rest []store.Item
			for _, k := range order[i:] {
				rest = append(rest, groups[k]...)
			}
			r.logf("no indexer can be searched until %s; %d items wait until then", until.Format("15:04"), len(rest))
			r.setSearch(rest, "notFound", "Indexers unavailable until "+until.Format("15:04"))
			time.AfterFunc(time.Until(until)+5*time.Second, r.Wake)
			return nil
		}
		m, err := r.st.Media(k.media)
		if err != nil {
			return err
		}
		if !m.IsShow() {
			if !r.grab(m, items, sceneWant(m, nil, 0, 0)) {
				r.exhausted[items[0].ID] = true
			}
			continue
		}
		all, err := r.st.Items(m.ID)
		if err != nil {
			return err
		}
		pack := sceneWant(m, all, k.season, 0)
		if len(items) > 1 && len(items) == pack.Count {
			if r.grab(m, items, pack) {
				continue
			}
			// No season pack; the episodes are searched one by one.
			r.setSearch(items, "queued", "")
		}
		for _, it := range items {
			r.serveRequests()
			if still, err := r.stillWanted([]store.Item{it}); err != nil {
				return err
			} else if len(still) == 0 {
				continue
			}
			season, episode := it.Scene()
			if !r.grab(m, []store.Item{it}, sceneWant(m, all, season, episode)) {
				r.exhausted[it.ID] = true
			}
		}
	}
	return nil
}

// grab searches for w and starts the best release that can be added.
func (r *runner) grab(m store.Media, items []store.Item, w release.Want) (ok bool) {
	if r.ctx.Err() != nil {
		return false
	}
	r.setSearch(items, "searching", "")
	r.mu.Lock()
	r.searching = &SearchView{MediaID: m.ID, Label: describe(m, items)}
	r.mu.Unlock()
	note := "No acceptable release found"
	defer func() {
		r.mu.Lock()
		r.searching = nil
		r.mu.Unlock()
		if ok {
			r.setSearch(items, "", "")
		} else {
			r.setSearch(items, "notFound", note)
		}
	}()
	r.logf("searching: %s", w.Query())
	rels, answered := searchAll(r.ctx, r.cfg(), w, r.logf, &r.indexers)
	time.Sleep(time.Second) // go easy on the indexers
	if !answered {
		note = "No indexer could be searched"
		r.logf("  no indexer could be searched")
		return false
	}
	cands := release.Rank(r.cfg().Quality, w, rels, r.st.Blocked)

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
		dl, err := r.start(ids, c.Title, c.Link, c.Size)
		if err != nil {
			r.logf("  skipping %s: %v", c.Title, err)
			continue
		}
		r.logf("  grabbed [%d] %s (%s, %d seeders)", dl.ID, c.Title, engine.Size(c.Size), c.Seeders)
		r.record(store.EventGrabbed, m.ID, describe(m, items),
			fmt.Sprintf("%s (%s, %d seeders, from %s)", c.Title, engine.Size(c.Size), c.Seeders, c.Indexer))
		return true
	}
	if tooBig > 0 {
		note = "Releases found would leave too little free space"
		r.logf("  %d acceptable releases would leave less than %s free", tooBig, engine.Size(int64(r.minFree())))
	} else {
		r.logf("  no acceptable release found")
	}
	return false
}

// start records a release as the download for the given items, and starts
// it, or queues it when all download slots are taken. size is the release's
// size as the indexer gave it, 0 when unknown.
func (r *runner) start(ids []int64, title, link string, size int64) (store.Download, error) {
	source, err := r.eng.Resolve(r.ctx, link)
	if err != nil {
		return store.Download{}, err
	}
	hash, err := engine.InfoHash(source)
	if err != nil {
		return store.Download{}, err
	}
	if r.st.HasInfoHash(hash) {
		// Same torrent as an earlier download, maybe under another name.
		return store.Download{}, errors.New("this torrent was grabbed before")
	}
	dl, err := r.st.CreateDownload(hash, title, source, ids)
	if err != nil {
		return store.Download{}, err
	}
	r.enqueue(dl, size)
	if err := r.fillSlots(); err != nil {
		return store.Download{}, err
	}
	if _, running := r.active[dl.ID]; !running {
		r.logf("[%d] queued; %d downloads are running", dl.ID, len(r.active))
	}
	r.publish()
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
	if _, _, best := engine.Swarm(a.t); best > 0 {
		a.seedSeen = time.Now()
	}
	if a.t.Complete().Bool() {
		return true, r.finish(a)
	}
	if at, why := giveUpAt(a.progress, a.seedSeen, a.lastDone); time.Now().After(at) {
		return true, r.fail(a, why)
	}
	return false, nil
}

// giveUpAt returns when a download is given up on, and why: once it has made
// no progress for stallTimeout, or sooner when it has nothing at all and no
// connected peer has had any of it for noSeederTimeout.
func giveUpAt(progress, seedSeen time.Time, done int64) (time.Time, string) {
	at, why := progress.Add(stallTimeout), "stalled"
	if done == 0 {
		if t := seedSeen.Add(noSeederTimeout); t.Before(at) {
			at, why = t, "no seeders"
		}
	}
	return at, why
}

func (r *runner) fail(a *activeDownload, why string) error {
	r.logf("[%d] failed (%s), blocklisting and looking for another release", a.dl.ID, why)
	r.notify("Download failed", fmt.Sprintf("%s failed (%s). Looking for another release.", a.dl.Name, why))
	r.recordDownload(store.EventFailed, a.dl.ID, a.dl.Name+": "+why)
	r.saveDownload(a)
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
				r.record(store.EventImported, m.ID, describe(m, []store.Item{it}), res.Dest)
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
	// Seeding resumes after a restart, which is quicker from the .torrent
	// file than from a magnet, whose metadata would come from peers again.
	if strings.HasPrefix(a.dl.Source, "magnet:") {
		if path, err := r.eng.SaveMetainfo(a.t); err != nil {
			r.logf("[%d] could not save its .torrent file: %v", a.dl.ID, err)
		} else if err := r.st.SetSource(a.dl.ID, path); err != nil {
			return err
		} else {
			a.dl.Source = path
		}
	}
	if err := r.st.SetSeeding(a.dl.ID, true); err != nil {
		return err
	}
	r.saveDownload(a)
	a.seedSince, a.paused = time.Now(), false
	r.seeding[a.dl.ID] = a
	return nil
}

// record adds an event to the history.
func (r *runner) record(event string, mediaID int64, title, detail string) {
	if err := r.st.AddEvent(event, mediaID, title, detail); err != nil {
		r.logf("could not add to the history: %v", err)
	}
}

// recordDownload adds an event about a download to the history, naming what
// it was meant to bring.
func (r *runner) recordDownload(event string, id int64, detail string) {
	items, _ := r.st.DownloadItems(id)
	if len(items) == 0 {
		mediaID := r.st.DownloadMedia(id)
		r.record(event, mediaID, r.mediaTitle(mediaID, ""), detail)
		return
	}
	m, err := r.st.Media(items[0].MediaID)
	if err != nil {
		r.record(event, 0, "", detail)
		return
	}
	r.record(event, m.ID, describe(m, items), detail)
}

// mediaTitle names a movie or show for the history, or returns fallback when
// it is not in the library.
func (r *runner) mediaTitle(mediaID int64, fallback string) string {
	m, err := r.st.Media(mediaID)
	if err != nil {
		return fallback
	}
	return describe(m, nil)
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

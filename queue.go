package main

import (
	"fmt"
	"math"
	"slices"
	"time"

	"unified/internal/config"
	"unified/internal/engine"
	"unified/internal/store"
)

// Only so many torrents run at once, as in qBittorrent: each running torrent
// brings its own peer connections, and dozens at once can overwhelm a home
// router. A queued download is recorded in the store like any other
// unfinished download, so it survives a restart, but it is not added to the
// client until a slot frees up, so it opens no connections meanwhile.
type queuedDownload struct {
	dl      store.Download
	mediaID int64
	size    int64 // as the indexer gave it; 0 when unknown
	// It could not start at once, so its start is worth a log line.
	waited bool
}

// A running torrent gets at least this many connections, however many share
// max_connections.
const minConnsPerTorrent = 5

// freeSlots is how many more torrents may run with limit allowed (0 is no
// limit) and running already running.
func freeSlots(limit, running int) int {
	if limit <= 0 {
		return math.MaxInt
	}
	return max(limit-running, 0)
}

// connsPerTorrent shares total connections among n running torrents, within
// the engine's own limit per torrent. A total of 0 is no overall limit.
func connsPerTorrent(total, n int) int {
	if total <= 0 || n <= 0 {
		return engine.MaxConnsPerTorrent
	}
	return min(engine.MaxConnsPerTorrent, max(minConnsPerTorrent, total/n))
}

// rateLimits returns the speed limits in force at now, in bytes per second
// (0 is no limit), and whether they are the alternative ones.
func rateLimits(c *config.Config, now time.Time) (down, up int64, alt bool) {
	if w, ok, _ := config.ParseWindow(c.AltSchedule); ok && w.Contains(now) {
		return int64(c.AltDownloadKBps) << 10, int64(c.AltUploadKBps) << 10, true
	}
	return int64(c.MaxDownloadKBps) << 10, int64(c.MaxUploadKBps) << 10, false
}

// LimitStatus is what holds torrents back, for the web UI.
type LimitStatus struct {
	Download     int64 `json:"download"` // bytes per second, 0 when unlimited
	Upload       int64 `json:"upload"`
	Alt          bool  `json:"alt"` // the alternative limits are in force
	MaxDownloads int   `json:"maxDownloads"`
	MaxSeeds     int   `json:"maxSeeds"`
}

// applyRateLimits sets the speed limits for now, logging when they change,
// as when the alternative limits start or end, or the settings are saved.
func (r *runner) applyRateLimits() {
	c := r.cfg()
	down, up, alt := rateLimits(c, time.Now())
	l := LimitStatus{Download: down, Upload: up, Alt: alt, MaxDownloads: c.MaxActiveDownloads, MaxSeeds: c.MaxActiveSeeds}
	r.mu.Lock()
	old, set := r.limits, r.limitsSet
	r.limits, r.limitsSet = l, true
	r.mu.Unlock()
	if set && old.Download == down && old.Upload == up {
		return
	}
	r.eng.SetRateLimits(down, up)
	if set || down > 0 || up > 0 {
		which := "speed limits"
		if alt {
			which = "alternative speed limits"
		}
		r.logf("%s: download %s, upload %s", which, speed(down), speed(up))
	}
}

func speed(bytesPerSecond int64) string {
	if bytesPerSecond <= 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d KiB/s", bytesPerSecond>>10)
}

// enqueue puts a download in line for a slot.
func (r *runner) enqueue(dl store.Download, size int64) {
	q := queuedDownload{dl: dl, size: size}
	if items, err := r.st.DownloadItems(dl.ID); err == nil && len(items) > 0 {
		q.mediaID = items[0].MediaID
	}
	r.queue = append(r.queue, q)
}

// unqueue takes downloads out of the line and returns them.
func (r *runner) unqueue(drop func(queuedDownload) bool) []queuedDownload {
	var out []queuedDownload
	r.queue = slices.DeleteFunc(r.queue, func(q queuedDownload) bool {
		if drop(q) {
			out = append(out, q)
			return true
		}
		return false
	})
	return out
}

// fillSlots starts queued downloads, oldest first, while there are free
// slots.
func (r *runner) fillSlots() error {
	for n := freeSlots(r.cfg().MaxActiveDownloads, len(r.active)); n > 0 && len(r.queue) > 0; {
		q := r.queue[0]
		r.queue = r.queue[1:]
		t, err := r.eng.Add(q.dl.Source)
		if err != nil {
			r.logf("[%d] cannot start %s: %v", q.dl.ID, q.dl.Name, err)
			if err := r.st.FailDownload(q.dl.ID); err != nil {
				return err
			}
			continue
		}
		if q.waited {
			r.logf("[%d] starting %s", q.dl.ID, q.dl.Name)
		}
		r.track(q.dl, q.mediaID, t)
		n--
	}
	for i := range r.queue {
		r.queue[i].waited = true
	}
	return nil
}

// limitSeeds lets the downloads that finished first seed, up to the limit,
// and pauses the rest until one of those stops. A paused seed stays in the
// client, so the data it uploaded still counts towards its seed ratio.
func (r *runner) limitSeeds() {
	seeds := make([]*activeDownload, 0, len(r.seeding))
	for _, a := range r.seeding {
		seeds = append(seeds, a)
	}
	slices.SortFunc(seeds, func(a, b *activeDownload) int {
		if c := a.seedSince.Compare(b.seedSince); c != 0 {
			return c
		}
		return int(a.dl.ID - b.dl.ID)
	})
	slots := freeSlots(r.cfg().MaxActiveSeeds, 0)
	for i, a := range seeds {
		switch run := i < slots; {
		case run && a.paused:
			a.t.AllowDataUpload()
			a.paused = false
			r.logf("[%d] seeding", a.dl.ID)
		case !run && !a.paused:
			a.t.DisallowDataUpload()
			a.paused = true
		}
	}
}

// balanceConns shares max_connections among the running torrents. Paused
// seeds get none, so they are not even connected to.
func (r *runner) balanceConns() {
	running := len(r.active)
	for _, a := range r.seeding {
		if !a.paused {
			running++
		}
	}
	per := connsPerTorrent(r.cfg().MaxConnections, running)
	set := func(a *activeDownload, n int) {
		if a.maxConns != n {
			a.t.SetMaxEstablishedConns(n)
			a.maxConns = n
		}
	}
	for _, a := range r.active {
		set(a, per)
	}
	for _, a := range r.seeding {
		if a.paused {
			set(a, 0)
		} else {
			set(a, per)
		}
	}
}

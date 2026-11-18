package main

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"unified/internal/config"
	"unified/internal/engine"
	"unified/internal/store"
)

func TestFreeSlots(t *testing.T) {
	for _, c := range []struct{ limit, running, want int }{
		{3, 0, 3}, {3, 2, 1}, {3, 3, 0}, {3, 5, 0}, {0, 7, math.MaxInt},
	} {
		if got := freeSlots(c.limit, c.running); got != c.want {
			t.Errorf("freeSlots(%d, %d) = %d, want %d", c.limit, c.running, got, c.want)
		}
	}
}

func TestConnsPerTorrent(t *testing.T) {
	for _, c := range []struct{ total, n, want int }{
		{200, 1, engine.MaxConnsPerTorrent}, // capped per torrent
		{200, 10, 20},
		{200, 100, minConnsPerTorrent}, // never starved
		{0, 10, engine.MaxConnsPerTorrent},
		{200, 0, engine.MaxConnsPerTorrent},
	} {
		if got := connsPerTorrent(c.total, c.n); got != c.want {
			t.Errorf("connsPerTorrent(%d, %d) = %d, want %d", c.total, c.n, got, c.want)
		}
	}
}

func TestRateLimits(t *testing.T) {
	c := &config.Config{MaxDownloadKBps: 4000, MaxUploadKBps: 500, AltDownloadKBps: 1000, AltUploadKBps: 100, AltSchedule: "08:00-23:00"}
	day := time.Date(2026, 11, 18, 12, 0, 0, 0, time.Local)
	night := time.Date(2026, 11, 18, 2, 0, 0, 0, time.Local)
	if down, up, alt := rateLimits(c, day); !alt || down != 1000<<10 || up != 100<<10 {
		t.Errorf("by day: %d %d %v", down, up, alt)
	}
	if down, up, alt := rateLimits(c, night); alt || down != 4000<<10 || up != 500<<10 {
		t.Errorf("at night: %d %d %v", down, up, alt)
	}
	c.AltSchedule = ""
	if _, _, alt := rateLimits(c, day); alt {
		t.Error("alternative limits without a schedule")
	}
}

// queueRunner has a show with three episodes, each grabbed and waiting in
// line, as when the download slots are full.
func queueRunner(t *testing.T) (*runner, *store.Store, int64, []store.Item) {
	dir := t.TempDir()
	cfg := &config.Config{DownloadDir: filepath.Join(dir, "dl"), MoviesDir: filepath.Join(dir, "movies"), TVDir: filepath.Join(dir, "tv")}
	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &runner{cfg: func() *config.Config { return cfg }, st: st, active: map[int64]*activeDownload{}, seeding: map[int64]*activeDownload{}}

	id, _ := st.AddMedia(store.Media{Kind: "show", TMDBID: 4, Title: "Show", Year: 2020})
	for e := 1; e <= 3; e++ {
		st.UpsertItem(id, 1, e, "", "2020-01-01", true)
	}
	items, _ := st.Items(id)
	for i, it := range items {
		hash := fmt.Sprintf("%040d", i+1)
		dl, err := st.CreateDownload(hash, fmt.Sprintf("Show.S01E%02d", it.Episode), "magnet:?xt=urn:btih:"+hash, []int64{it.ID})
		if err != nil {
			t.Fatal(err)
		}
		r.enqueue(dl, 1<<30)
	}
	return r, st, id, items
}

func TestQueueOrder(t *testing.T) {
	r, _, id, _ := queueRunner(t)
	r.publish()
	q := r.Snapshot().Queued
	if len(q) != 3 || q[0].Name != "Show.S01E01" || q[2].Position != 3 || q[1].MediaID != id {
		t.Fatalf("queued: %+v", q)
	}
	// A failed download leaves the line.
	r.unqueue(func(q queuedDownload) bool { return q.dl.Name == "Show.S01E02" })
	r.publish()
	if q := r.Snapshot().Queued; len(q) != 2 || q[1].Name != "Show.S01E03" || q[1].Position != 2 {
		t.Errorf("after removing one: %+v", q)
	}
}

func TestCancelQueued(t *testing.T) {
	r, st, id, items := queueRunner(t)
	// Cancelling episode 2 takes its download out of the line.
	if err := r.remove(removeRequest{media: id, items: []int64{items[1].ID}, cancel: true}); err != nil {
		t.Fatal(err)
	}
	if len(r.queue) != 2 {
		t.Fatalf("%d queued, want 2", len(r.queue))
	}
	if it, _ := st.Item(items[1].ID); it.State != store.Ignored {
		t.Errorf("cancelled episode is %s", it.State)
	}
	if dls, _ := st.Downloads(true); len(dls) != 2 {
		t.Errorf("%d unfinished downloads in the store, want 2", len(dls))
	}

	// Removing the show empties the line.
	if err := r.remove(removeRequest{media: id, deleteFiles: true, fromLibrary: true}); err != nil {
		t.Fatal(err)
	}
	if len(r.queue) != 0 {
		t.Errorf("%d still queued", len(r.queue))
	}
	if dls, _ := st.Downloads(true); len(dls) != 0 {
		t.Errorf("%d unfinished downloads left", len(dls))
	}
}

func TestGiveUpAt(t *testing.T) {
	start := time.Date(2026, 11, 18, 20, 0, 0, 0, time.Local)
	for _, c := range []struct {
		name     string
		seedSeen time.Time
		done     int64
		want     time.Time
		why      string
	}{
		{"nothing and no seeders", start, 0, start.Add(noSeederTimeout), "no seeders"},
		{"a peer has some lately", start.Add(18 * time.Minute), 0, start.Add(stallTimeout), "stalled"},
		{"has some already", start, 1 << 20, start.Add(stallTimeout), "stalled"},
	} {
		at, why := giveUpAt(start, c.seedSeen, c.done)
		if !at.Equal(c.want) || why != c.why {
			t.Errorf("%s: %v %q, want %v %q", c.name, at.Format("15:04"), why, c.want.Format("15:04"), c.why)
		}
	}
}

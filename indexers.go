package main

import (
	"regexp"
	"sync"
	"time"
)

// An indexer that fails is left alone for a while: Prowlarr's own pause when
// it says how long, otherwise this long.
const (
	indexerBackoff    = 5 * time.Minute
	maxIndexerBackoff = 6 * time.Hour
)

// indexerHealth remembers which indexers failed recently, so a search round
// does not ask a failing indexer again for every item. The zero value is
// ready to use, and a nil one keeps nothing.
type indexerHealth struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// down reports whether the indexer should be skipped, and until when.
func (h *indexerHealth) down(name string) (time.Time, bool) {
	if h == nil {
		return time.Time{}, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	until, ok := h.until[name]
	return until, ok && time.Now().Before(until)
}

// fail records that an indexer failed and returns until when it is skipped.
func (h *indexerHealth) fail(name string, err error) time.Time {
	now := time.Now()
	until := now.Add(indexerBackoff)
	if t, ok := pausedUntil(err.Error()); ok && t.After(until) {
		until = t
		if limit := now.Add(maxIndexerBackoff); until.After(limit) {
			until = limit
		}
	}
	if h == nil {
		return until
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.until == nil {
		h.until = map[string]time.Time{}
	}
	h.until[name] = until
	return until
}

// allDown reports whether every named indexer is being skipped, and when the
// first comes back.
func (h *indexerHealth) allDown(names []string) (time.Time, bool) {
	var first time.Time
	for _, n := range names {
		until, down := h.down(n)
		if !down {
			return time.Time{}, false
		}
		if first.IsZero() || until.Before(first) {
			first = until
		}
	}
	return first, len(names) > 0
}

// Prowlarr: "Indexer is disabled till 11/18/2026 7:43:09 PM due to recent failures."
var prowlarrPause = regexp.MustCompile(`disabled till (\d{1,2}/\d{1,2}/\d{4} \d{1,2}:\d{2}:\d{2} [AP]M)`)

func pausedUntil(msg string) (time.Time, bool) {
	m := prowlarrPause.FindStringSubmatch(msg)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("1/2/2006 3:04:05 PM", m[1], time.Local)
	return t, err == nil
}

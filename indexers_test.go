package main

import (
	"errors"
	"testing"
	"time"
)

func TestIndexerHealth(t *testing.T) {
	var h indexerHealth
	if _, down := h.down("a"); down {
		t.Fatal("an indexer that never failed is down")
	}

	pause := time.Now().Add(20 * time.Minute).Truncate(time.Second)
	msg := "prowlar: Indexer is disabled till " + pause.Format("1/2/2006 3:04:05 PM") + " due to recent failures."
	if until := h.fail("a", errors.New(msg)); !until.Equal(pause) {
		t.Errorf("paused until %v, want Prowlarr's %v", until, pause)
	}
	if until := h.fail("b", errors.New("connection refused")); time.Until(until) > indexerBackoff {
		t.Errorf("default backoff until %v", until)
	}

	if _, down := h.allDown([]string{"a", "c"}); down {
		t.Error("all down while c is fine")
	}
	until, down := h.allDown([]string{"a", "b"})
	if !down || time.Until(until) > indexerBackoff {
		t.Errorf("allDown = %v %v, want the earlier of the two", until, down)
	}

	// A nil health keeps nothing.
	var none *indexerHealth
	none.fail("a", errors.New("x"))
	if _, down := none.down("a"); down {
		t.Error("nil health remembered a failure")
	}
}

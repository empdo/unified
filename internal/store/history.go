package store

import (
	"strings"
	"time"
)

// History events.
const (
	EventAdded     = "added"     // added to the library, by hand or from a list
	EventScanned   = "scanned"   // files already on disk taken into the library
	EventGrabbed   = "grabbed"   // a release was chosen and started
	EventImported  = "imported"  // a file was linked into the library
	EventFailed    = "failed"    // a download was given up on and blocklisted
	EventCancelled = "cancelled" // downloads were stopped by hand
	EventDeleted   = "deleted"   // files were deleted
	EventRemoved   = "removed"   // removed from the library
	EventSeeded    = "seeded"    // stopped seeding at the seed ratio
)

// Event is one entry of the history.
type Event struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Event   string    `json:"event"`
	MediaID int64     `json:"mediaId,omitempty"`
	Title   string    `json:"title"`
	Detail  string    `json:"detail,omitempty"`
}

// AddEvent records an event in the history, at the current time.
func (s *Store) AddEvent(event string, mediaID int64, title, detail string) error {
	_, err := s.db.Exec(`INSERT INTO history (at, event, media_id, title, detail) VALUES (?, ?, ?, ?, ?)`,
		time.Now().Format(time.RFC3339), event, mediaID, title, detail)
	return err
}

// CountEvents counts the events of a kind since a time.
func (s *Store) CountEvents(event string, since time.Time) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM history WHERE event = ? AND at >= ?`, event, since.Format(time.RFC3339)).Scan(&n)
	return n
}

// LastEvents returns when each movie or show last had an event of a kind,
// for those that had one since a time.
func (s *Store) LastEvents(event string, since time.Time) (map[int64]time.Time, error) {
	rows, err := s.db.Query(`SELECT media_id, MAX(at) FROM history WHERE event = ? AND at >= ? GROUP BY media_id`,
		event, since.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var id int64
		var at string
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id], _ = time.Parse(time.RFC3339, at)
	}
	return out, rows.Err()
}

// HistoryQuery selects events, newest first.
type HistoryQuery struct {
	MediaID int64    // 0: about anything
	Events  []string // empty: every kind
	Before  int64    // only events older than this id; 0: from the newest
	Limit   int
}

// History returns the events q selects, newest first.
func (s *Store) History(q HistoryQuery) ([]Event, error) {
	where := []string{"1"}
	var args []any
	if q.MediaID != 0 {
		where = append(where, "media_id = ?")
		args = append(args, q.MediaID)
	}
	if len(q.Events) > 0 {
		where = append(where, "event IN (?"+strings.Repeat(", ?", len(q.Events)-1)+")")
		for _, e := range q.Events {
			args = append(args, e)
		}
	}
	if q.Before != 0 {
		where = append(where, "id < ?")
		args = append(args, q.Before)
	}
	args = append(args, max(q.Limit, 1))
	rows, err := s.db.Query(`SELECT id, at, event, media_id, title, detail FROM history WHERE `+
		strings.Join(where, " AND ")+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		var at string
		if err := rows.Scan(&e.ID, &at, &e.Event, &e.MediaID, &e.Title, &e.Detail); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, e)
	}
	return out, rows.Err()
}

package store

import (
	"database/sql"
	"time"
)

// List kinds.
const (
	TraktWatchlist = "trakt-watchlist" // source: a Trakt user name
	TraktList      = "trakt-list"      // source: user/list-slug
	TMDBList       = "tmdb-list"       // source: a TMDB list id
)

// What a list's new entries are added as.
const (
	WantAll   = "all"   // movies, and every episode of shows, are wanted
	WantFirst = "first" // movies, and the first season of shows
	WantNone  = "none"  // added, but nothing is wanted
)

// List is a watchlist or list whose movies and shows are added to the
// library as they appear on it.
type List struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Source    string    `json:"source"`
	Name      string    `json:"name"`
	Want      string    `json:"want"`
	SyncedAt  time.Time `json:"syncedAt,omitzero"`
	LastError string    `json:"lastError,omitempty"`
}

func (s *Store) AddList(l List) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO lists (kind, source, name, want) VALUES (?, ?, ?, ?)`, l.Kind, l.Source, l.Name, l.Want)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Lists() ([]List, error) {
	rows, err := s.db.Query(`SELECT id, kind, source, name, want, synced_at, last_error FROM lists ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []List{}
	for rows.Next() {
		var l List
		var synced string
		if err := rows.Scan(&l.ID, &l.Kind, &l.Source, &l.Name, &l.Want, &synced, &l.LastError); err != nil {
			return nil, err
		}
		l.SyncedAt, _ = time.Parse(time.RFC3339, synced)
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) List(id int64) (List, error) {
	lists, err := s.Lists()
	if err != nil {
		return List{}, err
	}
	for _, l := range lists {
		if l.ID == id {
			return l, nil
		}
	}
	return List{}, sql.ErrNoRows
}

func (s *Store) RemoveList(id int64) error {
	_, err := s.db.Exec(`DELETE FROM lists WHERE id = ?`, id)
	return err
}

// ListSynced records the outcome of a list's sync; errText is "" when it
// worked.
func (s *Store) ListSynced(id int64, errText string) error {
	_, err := s.db.Exec(`UPDATE lists SET synced_at = ?, last_error = ? WHERE id = ?`,
		time.Now().Format(time.RFC3339), errText, id)
	return err
}

// Seen reports whether a list's entry was handled before.
func (s *Store) Seen(listID int64, kind string, tmdbID int) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM list_seen WHERE list_id = ? AND kind = ? AND tmdb_id = ?`, listID, kind, tmdbID).Scan(&n)
	return n > 0
}

// MarkSeen records that a list's entry was handled.
func (s *Store) MarkSeen(listID int64, kind string, tmdbID int) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO list_seen (list_id, kind, tmdb_id) VALUES (?, ?, ?)`, listID, kind, tmdbID)
	return err
}

// MediaByTMDB returns the library's id for a TMDB movie or show, 0 when it
// is not in the library.
func (s *Store) MediaByTMDB(kind string, tmdbID int) int64 {
	var id int64
	s.db.QueryRow(`SELECT id FROM media WHERE kind = ? AND tmdb_id = ?`, kind, tmdbID).Scan(&id)
	return id
}

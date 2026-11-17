// Package store is the SQLite-backed library: media, the items (movies or
// episodes) that belong to them, and the downloads that fetch them.
package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Item states.
const (
	Ignored     = "ignored"
	Wanted      = "wanted"
	Downloading = "downloading"
	Imported    = "imported"
)

// Download states.
const (
	Active = "downloading"
	Done   = "done"
	Failed = "failed"
)

const schema = `
CREATE TABLE IF NOT EXISTS media (
	id      INTEGER PRIMARY KEY,
	kind    TEXT NOT NULL,            -- 'movie' or 'show'
	tmdb_id INTEGER NOT NULL,
	title   TEXT NOT NULL,
	year    INTEGER NOT NULL,
	poster   TEXT NOT NULL DEFAULT '',  -- TMDB image path, e.g. /abc.jpg
	overview TEXT NOT NULL DEFAULT '',
	UNIQUE (kind, tmdb_id)
);
-- A movie has exactly one item (season 0, episode 0); a show has one per episode.
CREATE TABLE IF NOT EXISTS items (
	id          INTEGER PRIMARY KEY,
	media_id    INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
	season      INTEGER NOT NULL,
	episode     INTEGER NOT NULL,
	title       TEXT NOT NULL DEFAULT '',
	air_date    TEXT NOT NULL DEFAULT '',
	state       TEXT NOT NULL,
	download_id INTEGER,
	path        TEXT NOT NULL DEFAULT '',
	UNIQUE (media_id, season, episode)
);
CREATE TABLE IF NOT EXISTS downloads (
	id       INTEGER PRIMARY KEY,
	infohash TEXT NOT NULL UNIQUE,
	name     TEXT NOT NULL,
	source   TEXT NOT NULL,           -- magnet URI or path to a saved .torrent
	state    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS blocklist (name TEXT PRIMARY KEY);
`

type Media struct {
	ID       int64
	Kind     string
	TMDBID   int
	Title    string
	Year     int
	Poster   string
	Overview string
}

func (m Media) IsShow() bool { return m.Kind == "show" }

type Item struct {
	ID       int64
	MediaID  int64
	Season   int
	Episode  int
	Title    string
	AirDate  string
	State    string
	Path     string
	Download sql.NullInt64
}

type Download struct {
	ID       int64
	InfoHash string
	Name     string
	Source   string
	State    string
}

type Store struct{ db *sql.DB }

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "unified.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

// migrate adds columns that databases created by older versions lack.
func migrate(db *sql.DB) error {
	cols := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('media')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		cols[name] = true
	}
	rows.Close()
	for _, col := range []string{"poster", "overview"} {
		if !cols[col] {
			if _, err := db.Exec(`ALTER TABLE media ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

// AddMedia inserts the media if it is new, or refreshes its details, and
// returns its id either way.
func (s *Store) AddMedia(m Media) (int64, error) {
	_, err := s.db.Exec(`INSERT INTO media (kind, tmdb_id, title, year, poster, overview) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (kind, tmdb_id) DO UPDATE SET
			title = excluded.title, year = excluded.year,
			poster = excluded.poster, overview = excluded.overview`,
		m.Kind, m.TMDBID, m.Title, m.Year, m.Poster, m.Overview)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRow(`SELECT id FROM media WHERE kind = ? AND tmdb_id = ?`, m.Kind, m.TMDBID).Scan(&id)
	return id, err
}

// SetArt fills in the poster and overview of media added before they were stored.
func (s *Store) SetArt(id int64, poster, overview string) error {
	_, err := s.db.Exec(`UPDATE media SET poster = ?, overview = ? WHERE id = ?`, poster, overview, id)
	return err
}

// UpsertItem adds an item, or refreshes its metadata. An existing item only
// changes state when it goes from ignored to wanted.
func (s *Store) UpsertItem(mediaID int64, season, episode int, title, airDate string, want bool) error {
	state := Ignored
	if want {
		state = Wanted
	}
	_, err := s.db.Exec(`INSERT INTO items (media_id, season, episode, title, air_date, state) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (media_id, season, episode) DO UPDATE SET
			title = excluded.title,
			air_date = excluded.air_date,
			state = CASE WHEN items.state = 'ignored' AND excluded.state = 'wanted' THEN 'wanted' ELSE items.state END`,
		mediaID, season, episode, title, airDate, state)
	return err
}

// SetSeasonWanted wants every ignored episode of a season, or with want
// false, stops wanting the ones not yet grabbed.
func (s *Store) SetSeasonWanted(mediaID int64, season int, want bool) error {
	from, to := Ignored, Wanted
	if !want {
		from, to = Wanted, Ignored
	}
	_, err := s.db.Exec(`UPDATE items SET state = ? WHERE media_id = ? AND season = ? AND state = ?`,
		to, mediaID, season, from)
	return err
}

// SetItemWanted wants an ignored item, or with want false, stops wanting it
// if it has not been grabbed yet.
func (s *Store) SetItemWanted(itemID int64, want bool) error {
	from, to := Ignored, Wanted
	if !want {
		from, to = Wanted, Ignored
	}
	_, err := s.db.Exec(`UPDATE items SET state = ? WHERE id = ? AND state = ?`, to, itemID, from)
	return err
}

const mediaCols = `id, kind, tmdb_id, title, year, poster, overview`

func scanMedia(row interface{ Scan(...any) error }) (Media, error) {
	var m Media
	err := row.Scan(&m.ID, &m.Kind, &m.TMDBID, &m.Title, &m.Year, &m.Poster, &m.Overview)
	return m, err
}

func (s *Store) Media(id int64) (Media, error) {
	return scanMedia(s.db.QueryRow(`SELECT `+mediaCols+` FROM media WHERE id = ?`, id))
}

func (s *Store) AllMedia() ([]Media, error) {
	rows, err := s.db.Query(`SELECT ` + mediaCols + ` FROM media ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Media
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RemoveMedia(id int64) error {
	_, err := s.db.Exec(`DELETE FROM media WHERE id = ?`, id)
	return err
}

const itemCols = `id, media_id, season, episode, title, air_date, state, path, download_id`

func (s *Store) queryItems(where string, args ...any) ([]Item, error) {
	rows, err := s.db.Query(`SELECT `+itemCols+` FROM items WHERE `+where+` ORDER BY media_id, season, episode`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var i Item
		if err := rows.Scan(&i.ID, &i.MediaID, &i.Season, &i.Episode, &i.Title, &i.AirDate, &i.State, &i.Path, &i.Download); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) Items(mediaID int64) ([]Item, error) {
	return s.queryItems(`media_id = ?`, mediaID)
}

// WantedItems returns wanted items that should exist by now: every wanted
// movie, and wanted episodes whose air date has passed.
func (s *Store) WantedItems() ([]Item, error) {
	today := time.Now().Format("2006-01-02")
	return s.queryItems(`state = 'wanted' AND (
		(season = 0 AND episode = 0) OR (air_date != '' AND air_date <= ?))`, today)
}

func (s *Store) DownloadItems(downloadID int64) ([]Item, error) {
	return s.queryItems(`download_id = ? AND state = 'downloading'`, downloadID)
}

// CreateDownload records a grabbed release and moves its items to downloading.
func (s *Store) CreateDownload(infohash, name, source string, itemIDs []int64) (Download, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Download{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO downloads (infohash, name, source, state) VALUES (?, ?, ?, ?)`,
		infohash, name, source, Active)
	if err != nil {
		return Download{}, err
	}
	id, _ := res.LastInsertId()
	for _, item := range itemIDs {
		if _, err := tx.Exec(`UPDATE items SET state = ?, download_id = ? WHERE id = ?`, Downloading, id, item); err != nil {
			return Download{}, err
		}
	}
	return Download{ID: id, InfoHash: infohash, Name: name, Source: source, State: Active}, tx.Commit()
}

func (s *Store) Downloads(onlyActive bool) ([]Download, error) {
	q := `SELECT id, infohash, name, source, state FROM downloads`
	if onlyActive {
		q += ` WHERE state = 'downloading'`
	}
	rows, err := s.db.Query(q + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Download
	for rows.Next() {
		var d Download
		if err := rows.Scan(&d.ID, &d.InfoHash, &d.Name, &d.Source, &d.State); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) HasInfoHash(infohash string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM downloads WHERE infohash = ?`, strings.ToLower(infohash)).Scan(&n)
	return n > 0
}

func (s *Store) SetImported(itemID int64, path string) error {
	_, err := s.db.Exec(`UPDATE items SET state = ?, path = ? WHERE id = ?`, Imported, path, itemID)
	return err
}

// FinishDownload marks the download done. Items it was meant to provide but
// that were not imported go back to wanted.
func (s *Store) FinishDownload(id int64) error {
	return s.closeDownload(id, Done, false)
}

// FailDownload blocklists the release and returns its items to wanted.
func (s *Store) FailDownload(id int64) error {
	return s.closeDownload(id, Failed, true)
}

func (s *Store) closeDownload(id int64, state string, block bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if block {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO blocklist (name) SELECT name FROM downloads WHERE id = ?`, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE items SET state = 'wanted', download_id = NULL WHERE download_id = ? AND state = 'downloading'`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE downloads SET state = ? WHERE id = ?`, state, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Blocked(name string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM blocklist WHERE name = ?`, name).Scan(&n)
	return n > 0
}

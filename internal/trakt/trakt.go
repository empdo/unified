// Package trakt reads public watchlists and lists from trakt.tv.
package trakt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	ID   string // the Client ID of a Trakt API app
	Base string
	HTTP *http.Client
}

func New(clientID string) *Client {
	return &Client{ID: clientID, Base: "https://api.trakt.tv", HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Entry is a movie or show on a list. Entries Trakt has no TMDB id for are
// left out.
type Entry struct {
	Kind   string // "movie" or "show"
	TMDBID int
	Title  string
	Year   int
}

type media struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	IDs   struct {
		TMDB int `json:"tmdb"`
	} `json:"ids"`
}

type item struct {
	Type  string `json:"type"`
	Movie *media `json:"movie"`
	Show  *media `json:"show"`
}

// Watchlist returns the movies and shows on a user's watchlist. The user's
// profile must be public.
func (c *Client) Watchlist(ctx context.Context, user string) ([]Entry, error) {
	return c.items(ctx, "/users/"+url.PathEscape(user)+"/watchlist/movie,show")
}

// List returns the movies and shows on a user's list, given as
// "user/list-slug" or the list's address on trakt.tv.
func (c *Client) List(ctx context.Context, list string) ([]Entry, error) {
	user, slug, err := ParseList(list)
	if err != nil {
		return nil, err
	}
	return c.items(ctx, "/users/"+url.PathEscape(user)+"/lists/"+url.PathEscape(slug)+"/items/movie,show")
}

// ParseList splits "user/list-slug", or a list's address such as
// https://trakt.tv/users/someone/lists/favourites, into user and slug.
func ParseList(list string) (user, slug string, err error) {
	s := strings.TrimSpace(list)
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		s = u.Path
	}
	s = strings.Trim(s, "/")
	s = strings.TrimPrefix(s, "users/")
	parts := strings.Split(s, "/")
	if len(parts) == 3 && parts[1] == "lists" {
		parts = []string{parts[0], parts[2]}
	}
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("a Trakt list is user/list-name or its trakt.tv address, not %q", list)
	}
	return parts[0], parts[1], nil
}

func (c *Client) items(ctx context.Context, path string) ([]Entry, error) {
	if c.ID == "" {
		return nil, fmt.Errorf("trakt_client_id is not set in the config")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.Base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", c.ID)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("trakt: not found; check the name, and that the profile is public")
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("trakt: %s; check trakt_client_id, and that the profile is public", resp.Status)
	default:
		return nil, fmt.Errorf("trakt: %s", resp.Status)
	}
	var items []item
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("trakt: %w", err)
	}
	var out []Entry
	for _, it := range items {
		kind, m := "movie", it.Movie
		if it.Type == "show" {
			kind, m = "show", it.Show
		}
		if m == nil || m.IDs.TMDB == 0 {
			continue
		}
		out = append(out, Entry{Kind: kind, TMDBID: m.IDs.TMDB, Title: m.Title, Year: m.Year})
	}
	return out, nil
}

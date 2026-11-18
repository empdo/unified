// Package tmdb looks up movies, shows and episode lists on themoviedb.org.
package tmdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	Key  string
	Base string
	HTTP *http.Client
}

func New(key string) *Client {
	return &Client{Key: key, Base: "https://api.themoviedb.org/3", HTTP: &http.Client{Timeout: 20 * time.Second}}
}

type Result struct {
	ID       int
	Title    string
	Year     int
	Poster   string // image path, e.g. "/abc.jpg"; see ImageURL
	Overview string
}

// ImageURL is the address of a TMDB image path at a given width, such as
// "w342" or "original".
func ImageURL(path, size string) string {
	if path == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + path
}

// result is the shape movies and shows share in TMDB responses; movies use
// title and release_date, shows name and first_air_date.
type result struct {
	ID           int    `json:"id"`
	Title        string `json:"title"`
	Name         string `json:"name"`
	ReleaseDate  string `json:"release_date"`
	FirstAirDate string `json:"first_air_date"`
	PosterPath   string `json:"poster_path"`
	Overview     string `json:"overview"`
}

func (r result) toResult() Result {
	return Result{
		ID:       r.ID,
		Title:    r.Title + r.Name,
		Year:     year(r.ReleaseDate + r.FirstAirDate),
		Poster:   r.PosterPath,
		Overview: r.Overview,
	}
}

type Episode struct {
	Season  int
	Number  int
	Title   string
	AirDate string
}

func (c *Client) get(path string, q url.Values, out any) error {
	if c.Key == "" {
		return fmt.Errorf("tmdb_api_key is not set in the config")
	}
	if q == nil {
		q = url.Values{}
	}
	req, err := http.NewRequest("GET", c.Base+path, nil)
	if err != nil {
		return err
	}
	// v4 read access tokens are long JWTs; v3 keys are 32 hex characters.
	if len(c.Key) > 40 {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	} else {
		q.Set("api_key", c.Key)
	}
	req.URL.RawQuery = q.Encode()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("tmdb: %s for %s", resp.Status, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Search finds movies (kind "movie") or shows (kind "show") by title.
func (c *Client) Search(kind, query string) ([]Result, error) {
	path := "/search/movie"
	if kind == "show" {
		path = "/search/tv"
	}
	var body struct {
		Results []result `json:"results"`
	}
	if err := c.get(path, url.Values{"query": {query}}, &body); err != nil {
		return nil, err
	}
	var out []Result
	for _, r := range body.Results {
		out = append(out, r.toResult())
	}
	return out, nil
}

// Details looks up one movie (kind "movie") or show (kind "show") by id.
func (c *Client) Details(kind string, id int) (Result, error) {
	path := "/movie/"
	if kind == "show" {
		path = "/tv/"
	}
	var r result
	if err := c.get(path+strconv.Itoa(id), nil, &r); err != nil {
		return Result{}, err
	}
	return r.toResult(), nil
}

// TVDBID returns the TheTVDB id of a show, or 0 when TMDB does not know it.
func (c *Client) TVDBID(showID int) (int, error) {
	var ids struct {
		TVDB int `json:"tvdb_id"`
	}
	err := c.get("/tv/"+strconv.Itoa(showID)+"/external_ids", nil, &ids)
	return ids.TVDB, err
}

// Episodes returns every episode of a show, skipping specials (season 0).
func (c *Client) Episodes(showID int) ([]Episode, error) {
	var show struct {
		Seasons []struct {
			Number int `json:"season_number"`
		} `json:"seasons"`
	}
	if err := c.get("/tv/"+strconv.Itoa(showID), nil, &show); err != nil {
		return nil, err
	}
	var out []Episode
	for _, s := range show.Seasons {
		if s.Number == 0 {
			continue
		}
		var season struct {
			Episodes []struct {
				Number  int    `json:"episode_number"`
				Name    string `json:"name"`
				AirDate string `json:"air_date"`
			} `json:"episodes"`
		}
		if err := c.get(fmt.Sprintf("/tv/%d/season/%d", showID, s.Number), nil, &season); err != nil {
			return nil, err
		}
		for _, e := range season.Episodes {
			out = append(out, Episode{Season: s.Number, Number: e.Number, Title: e.Name, AirDate: e.AirDate})
		}
	}
	return out, nil
}

func year(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(date[:4])
	return y
}

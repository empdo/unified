// Package xem looks up scene numbering and scene names on thexem.info, for
// shows whose releases are numbered differently from TVDB (often anime).
package xem

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type Client struct {
	Base string
	HTTP *http.Client
}

func New() *Client {
	return &Client{Base: "https://thexem.info", HTTP: &http.Client{Timeout: 20 * time.Second}}
}

type Episode struct {
	Season   int `json:"season"`
	Episode  int `json:"episode"`
	Absolute int `json:"absolute"`
}

// Mapping pairs an episode's TVDB numbering with the numbering releases use.
type Mapping struct {
	TVDB  Episode `json:"tvdb"`
	Scene Episode `json:"scene"`
}

func (c *Client) get(path string, tvdbID int, out any) error {
	req, err := http.NewRequest("GET", c.Base+path+"?origin=tvdb&id="+strconv.Itoa(tvdbID), nil)
	if err != nil {
		return err
	}
	// Requests without a user agent are turned away by Cloudflare.
	req.Header.Set("User-Agent", "unified")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("xem: %s for %s", resp.Status, path)
	}
	var body struct {
		Result  string          `json:"result"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("xem: %s: %w", path, err)
	}
	// Shows XEM knows nothing about come back as a failure with no data.
	if body.Result != "success" {
		return nil
	}
	return json.Unmarshal(body.Data, out)
}

// Map returns the scene numbering of a show's episodes, or nothing when the
// show has no mapping (its releases follow TVDB).
func (c *Client) Map(tvdbID int) ([]Mapping, error) {
	var out []Mapping
	return out, c.get("/map/all", tvdbID, &out)
}

// Names returns the other titles releases of a show go by, such as the
// romanized Japanese title of an anime. Names that only apply to one season
// are left out.
func (c *Client) Names(tvdbID int) ([]string, error) {
	var data map[string]map[string][]string
	if err := c.get("/map/names", tvdbID, &data); err != nil {
		return nil, err
	}
	var out []string
	for _, names := range data["all"] {
		out = append(out, names...)
	}
	return out, nil
}

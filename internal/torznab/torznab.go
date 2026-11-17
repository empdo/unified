// Package torznab searches Torznab indexers (Prowlarr, Jackett, or trackers
// that speak the protocol themselves).
package torznab

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"unified/internal/config"
)

// Standard Torznab category ids.
const (
	Movies = 2000
	TV     = 5000
)

type Release struct {
	Indexer string
	Title   string
	// Link is a magnet URI or a URL that serves a .torrent file.
	Link    string
	Size    int64
	Seeders int // -1 when the indexer does not report it
}

type feed struct {
	Items []struct {
		Title     string `xml:"title"`
		Link      string `xml:"link"`
		Size      int64  `xml:"size"`
		Enclosure struct {
			URL    string `xml:"url,attr"`
			Length int64  `xml:"length,attr"`
		} `xml:"enclosure"`
		Attrs []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"attr"`
	} `xml:"channel>item"`
}

var client = &http.Client{Timeout: 30 * time.Second}

func Search(ctx context.Context, ix config.Indexer, query string, category int) ([]Release, error) {
	body, err := get(ctx, ix, url.Values{
		"t":     {"search"},
		"q":     {query},
		"cat":   {strconv.Itoa(category)},
		"limit": {"100"},
	})
	if err != nil {
		return nil, err
	}
	var f feed
	if err := xml.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("%s: not a Torznab response, check the url (for Prowlarr it ends in /<id>/api)", ix.Name)
	}

	var out []Release
	for _, it := range f.Items {
		r := Release{Indexer: ix.Name, Title: it.Title, Size: it.Size, Seeders: -1}
		r.Link = it.Enclosure.URL
		if r.Link == "" {
			r.Link = it.Link
		}
		if r.Size == 0 {
			r.Size = it.Enclosure.Length
		}
		for _, a := range it.Attrs {
			switch a.Name {
			case "seeders":
				if n, err := strconv.Atoi(a.Value); err == nil {
					r.Seeders = n
				}
			case "size":
				if n, err := strconv.ParseInt(a.Value, 10, 64); err == nil && r.Size == 0 {
					r.Size = n
				}
			case "magneturl":
				if a.Value != "" {
					r.Link = a.Value
				}
			}
		}
		if r.Title != "" && r.Link != "" {
			out = append(out, r)
		}
	}
	return out, nil
}

// Test checks that the indexer answers Torznab requests with this API key.
func Test(ctx context.Context, ix config.Indexer) error {
	body, err := get(ctx, ix, url.Values{"t": {"caps"}})
	if err != nil {
		return err
	}
	var root struct{ XMLName xml.Name }
	if xml.Unmarshal(body, &root) != nil || root.XMLName.Local != "caps" {
		return fmt.Errorf("%s: not a Torznab response, check the url (for Prowlarr it ends in /<id>/api)", ix.Name)
	}
	return nil
}

// get sends a Torznab request and returns the XML body, turning HTTP and
// Torznab errors into readable ones.
func get(ctx context.Context, ix config.Indexer, q url.Values) ([]byte, error) {
	u, err := url.Parse(ix.URL)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid url: %w", ix.Name, err)
	}
	query := u.Query()
	for k, v := range q {
		query[k] = v
	}
	if ix.APIKey != "" {
		query.Set("apikey", ix.APIKey)
	}
	u.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ix.Name, redact(err, ix.APIKey))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ix.Name, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%s: %s, check the API key", ix.Name, resp.Status)
	}
	var e struct {
		XMLName     xml.Name
		Description string `xml:"description,attr"`
	}
	if xml.Unmarshal(body, &e) == nil && e.XMLName.Local == "error" {
		return nil, fmt.Errorf("%s: %s", ix.Name, e.Description)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", ix.Name, resp.Status)
	}
	return body, nil
}

// redact keeps the API key out of error messages, which include the URL.
func redact(err error, key string) error {
	if uerr, ok := err.(*url.Error); ok && key != "" {
		if u, perr := url.Parse(uerr.URL); perr == nil {
			q := u.Query()
			q.Set("apikey", "REDACTED")
			u.RawQuery = q.Encode()
			uerr.URL = u.String()
		}
	}
	return err
}

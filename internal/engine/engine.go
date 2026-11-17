// Package engine wraps the embedded BitTorrent client.
package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

type Engine struct {
	client     *torrent.Client
	torrentDir string
	vpn        *binding
}

// New starts a client that stores data in downloadDir and keeps fetched
// .torrent files in torrentDir. With vpnInterface set, all torrent traffic
// (peers, DHT, trackers, web seeds) is bound to that interface's addresses,
// and New fails if the interface is not up.
func New(downloadDir, torrentDir string, port int, vpnInterface string) (*Engine, error) {
	for _, d := range []string{downloadDir, torrentDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = downloadDir
	cfg.ListenPort = port
	cfg.Seed = true
	cfg.Slogger = slog.New(slog.DiscardHandler)
	var vpn *binding
	if vpnInterface != "" {
		var err error
		if vpn, err = bindInterface(vpnInterface); err != nil {
			return nil, err
		}
		vpn.configure(cfg)
	}
	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	if vpn != nil {
		vpn.addPeerDialers(client)
	}
	return &Engine{client: client, torrentDir: torrentDir, vpn: vpn}, nil
}

// VPN reports the interface and addresses torrent traffic is bound to ("" when
// no VPN is configured), and an error if the interface has gone down or
// changed address since startup. Traffic never falls back to another
// interface, so while the error is set, torrents simply make no progress.
func (e *Engine) VPN() (desc string, err error) {
	if e.vpn == nil {
		return "", nil
	}
	return e.vpn.String(), e.vpn.check()
}

func (e *Engine) Close() { e.client.Close() }

// Resolve turns an indexer link into something Add accepts: a magnet URI is
// returned as is, a .torrent URL is downloaded and saved, and its path returned.
// Indexer proxies often redirect a download URL to a magnet, so that is handled too.
func (e *Engine) Resolve(ctx context.Context, link string) (string, error) {
	if strings.HasPrefix(link, "magnet:") {
		return link, nil
	}
	hc := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme == "magnet" {
				return http.ErrUseLastResponse
			}
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching torrent: %w", err)
	}
	defer resp.Body.Close()
	if loc := resp.Header.Get("Location"); strings.HasPrefix(loc, "magnet:") {
		return loc, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching torrent: %s", resp.Status)
	}
	mi, err := metainfo.Load(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return "", fmt.Errorf("not a valid .torrent file: %w", err)
	}
	path := filepath.Join(e.torrentDir, mi.HashInfoBytes().HexString()+".torrent")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return path, mi.Write(f)
}

// Add registers a magnet URI or a saved .torrent file. The download itself
// starts once Start is called, which needs the torrent's metadata.
func (e *Engine) Add(source string) (*torrent.Torrent, error) {
	if strings.HasPrefix(source, "magnet:") {
		return e.client.AddMagnet(source)
	}
	return e.client.AddTorrentFromFile(source)
}

// HasInfo reports whether the torrent's metadata (name, file list) is known yet.
func HasInfo(t *torrent.Torrent) bool {
	select {
	case <-t.GotInfo():
		return true
	default:
		return false
	}
}

// Status is a one-line progress summary.
func Status(t *torrent.Torrent) string {
	if !HasInfo(t) {
		return fmt.Sprintf("fetching metadata (%d peers)", t.Stats().ActivePeers)
	}
	done, total := t.BytesCompleted(), t.Length()
	pct := 100.0
	if total > 0 {
		pct = float64(done) / float64(total) * 100
	}
	return fmt.Sprintf("%5.1f%%  %s / %s  %d peers", pct, Size(done), Size(total), t.Stats().ActivePeers)
}

func Size(n int64) string {
	const mb = 1 << 20
	if n >= 1<<30 {
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%.1f MB", float64(n)/mb)
}

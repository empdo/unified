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
	"golang.org/x/time/rate"
)

// Connection limits, lower than the library's (50 per torrent, 25 and 100
// half-open), which add up to hundreds of connections with a few torrents
// and can fill a home router's NAT table. qBittorrent allows 100 per torrent
// but has far fewer torrents running at once.
const (
	MaxConnsPerTorrent      = 30
	halfOpenConnsPerTorrent = 10
	totalHalfOpenConns      = 50
)

// Bursts for the rate limiters. A peer may have this much requested from us
// at once, and the upload burst must cover it even while the limit changes,
// or the library panics; the library's 1 MiB would let a low limit burst far
// past itself. The download burst is about a second of traffic, but at
// least one 16 KiB chunk.
const (
	maxPeerRequestData = 256 << 10
	minDownloadBurst   = 16 << 10
	maxDownloadBurst   = 1 << 20
)

type Engine struct {
	client     *torrent.Client
	torrentDir string
	vpn        *binding
	// Shared by all torrents; changed with SetRateLimits.
	down, up *rate.Limiter
}

// New starts a client that stores data in downloadDir and keeps fetched
// .torrent files in torrentDir. With vpnInterface set, all torrent traffic
// (peers, DHT, trackers, web seeds) is bound to that interface's addresses,
// and New fails if the interface is not up. Transfers are not rate limited
// until SetRateLimits is called.
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
	cfg.EstablishedConnsPerTorrent = MaxConnsPerTorrent
	cfg.HalfOpenConnsPerTorrent = halfOpenConnsPerTorrent
	cfg.TotalHalfOpenConns = totalHalfOpenConns
	cfg.MaxAllocPeerRequestDataPerConn = maxPeerRequestData
	// Both limiters exist from the start: the library reads them once, and
	// a nil download limiter could never be turned on later.
	up := rate.NewLimiter(rate.Inf, maxPeerRequestData)
	down := rate.NewLimiter(rate.Inf, downloadBurst(0))
	cfg.UploadRateLimiter, cfg.DownloadRateLimiter = up, down
	// Outgoing peer connections go through addPeerDialers, which prefers uTP.
	cfg.DialForPeerConns = false
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
	tcp := tcpDialers(client)
	if vpn != nil {
		tcp = vpn.tcpDialers()
	}
	addPeerDialers(client, tcp)
	return &Engine{client: client, torrentDir: torrentDir, vpn: vpn, down: down, up: up}, nil
}

// SetRateLimits limits all downloading and uploading, in bytes per second;
// 0 is no limit. It applies at once, to transfers already running too.
func (e *Engine) SetRateLimits(down, up int64) {
	e.down.SetBurst(downloadBurst(down))
	e.down.SetLimit(limit(down))
	// The burst stays at maxPeerRequestData, as requests already accepted
	// while there was no limit can be that long.
	e.up.SetLimit(limit(up))
}

func limit(bytesPerSecond int64) rate.Limit {
	if bytesPerSecond <= 0 {
		return rate.Inf
	}
	return rate.Limit(bytesPerSecond)
}

func downloadBurst(bytesPerSecond int64) int {
	if bytesPerSecond <= 0 {
		return maxDownloadBurst
	}
	return int(max(minDownloadBurst, min(bytesPerSecond, maxDownloadBurst)))
}

// InfoHash returns the info hash of a magnet URI or a saved .torrent file,
// without adding it to the client.
func InfoHash(source string) (string, error) {
	if strings.HasPrefix(source, "magnet:") {
		m, err := metainfo.ParseMagnetUri(source)
		if err != nil {
			return "", fmt.Errorf("not a valid magnet link: %w", err)
		}
		return m.InfoHash.HexString(), nil
	}
	mi, err := metainfo.LoadFromFile(source)
	if err != nil {
		return "", fmt.Errorf("not a valid .torrent file: %w", err)
	}
	return mi.HashInfoBytes().HexString(), nil
}

// Swarm describes who a torrent is connected to: connected peers that have
// the whole torrent, peers known from trackers, DHT and other peers, and
// the largest share of the torrent (0 to 1) any connected peer has. A
// download stuck at 0% with best at 0 has no one to download from.
func Swarm(t *torrent.Torrent) (seeders, known int, best float64) {
	st := t.Stats()
	if HasInfo(t) && t.NumPieces() > 0 {
		for _, c := range t.PeerConns() {
			best = max(best, float64(c.Stats().RemotePieceCount)/float64(t.NumPieces()))
		}
	}
	return st.ConnectedSeeders, st.TotalPeers, best
}

// WriteStatus writes the client's own report on every torrent and peer
// connection: which peers choke us, what they have, rates. For debugging.
func (e *Engine) WriteStatus(w io.Writer) { e.client.WriteStatus(w) }

// Conns counts a torrent's peer connections by transport: uTP, TCP, and
// anything else (WebRTC).
func Conns(t *torrent.Torrent) (utp, tcp, other int) {
	for _, c := range t.PeerConns() {
		switch {
		case strings.HasPrefix(c.Network, "udp"):
			utp++
		case strings.HasPrefix(c.Network, "tcp"):
			tcp++
		default:
			other++
		}
	}
	return utp, tcp, other
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

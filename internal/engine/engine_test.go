package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

func TestSetRateLimits(t *testing.T) {
	dir := t.TempDir()
	e, err := New(filepath.Join(dir, "dl"), filepath.Join(dir, "torrents"), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.down.Limit() != rate.Inf || e.up.Limit() != rate.Inf {
		t.Fatal("limited before any limit was set")
	}

	e.SetRateLimits(500<<10, 100<<10)
	if e.down.Limit() != 500<<10 || e.up.Limit() != 100<<10 {
		t.Errorf("limits %v %v", e.down.Limit(), e.up.Limit())
	}
	// The upload burst must cover any request a peer may have open, or the
	// library panics; the download burst at least a chunk.
	if e.up.Burst() < maxPeerRequestData || e.down.Burst() < minDownloadBurst || e.down.Burst() > maxDownloadBurst {
		t.Errorf("bursts: up %d, down %d", e.up.Burst(), e.down.Burst())
	}

	e.SetRateLimits(1<<10, 0)
	if e.down.Burst() != minDownloadBurst || e.up.Limit() != rate.Inf {
		t.Errorf("tiny download limit: burst %d; upload %v", e.down.Burst(), e.up.Limit())
	}
	e.SetRateLimits(0, 0)
	if e.down.Limit() != rate.Inf || e.down.Burst() == 0 {
		// A zero burst would read as no tokens, and the library would open
		// no connections.
		t.Errorf("unlimited again: %v, burst %d", e.down.Limit(), e.down.Burst())
	}
}

func TestInfoHash(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	if got, err := InfoHash("magnet:?xt=urn:btih:" + hash + "&dn=x"); err != nil || got != hash {
		t.Errorf("magnet: %q, %v", got, err)
	}

	info := metainfo.Info{Name: "x", PieceLength: 16 << 10, Pieces: make([]byte, 20), Length: 1}
	var mi metainfo.MetaInfo
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "x.torrent")
	f, _ := os.Create(path)
	mi.Write(f)
	f.Close()
	if got, err := InfoHash(path); err != nil || got != mi.HashInfoBytes().HexString() {
		t.Errorf(".torrent: %q, %v", got, err)
	}

	if _, err := InfoHash("magnet:?dn=no-hash"); err == nil {
		t.Error("a magnet without a hash gave no error")
	}
}

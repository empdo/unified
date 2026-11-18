package engine

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
)

// utpHeadStart is how long a TCP dial waits for uTP to the same peer. The
// library dials every network at once and keeps the first to connect, which
// is nearly always TCP. TCP uses the kernel's congestion control, which keeps
// the router's queues full and makes everything else on the network slow;
// uTP backs off as soon as it sees delay building up (LEDBAT). Waiting lets
// uTP win whenever the peer speaks it, with TCP as the fallback.
const utpHeadStart = 2 * time.Second

var errUTPConnected = errors.New("connected over uTP instead")

// addPeerDialers sets up outgoing peer connections: uTP through the client's
// own UDP sockets, and TCP through tcp, held back for utpHeadStart.
func addPeerDialers(client *torrent.Client, tcp []torrent.Dialer) {
	race := &dialRace{}
	for _, l := range client.Listeners() {
		if d, ok := l.(torrent.Dialer); ok && strings.HasPrefix(d.DialerNetwork(), "udp") {
			client.AddDialer(utpDialer{d, race})
		}
	}
	for _, d := range tcp {
		client.AddDialer(tcpFallback{d, race, utpHeadStart})
	}
}

// tcpDialers returns the TCP sockets the client listens on, which also dial
// out from the listening port.
func tcpDialers(client *torrent.Client) []torrent.Dialer {
	var out []torrent.Dialer
	for _, l := range client.Listeners() {
		if d, ok := l.(torrent.Dialer); ok && strings.HasPrefix(d.DialerNetwork(), "tcp") {
			out = append(out, d)
		}
	}
	return out
}

type utpDialer struct {
	torrent.Dialer
	race *dialRace
}

func (d utpDialer) Dial(ctx context.Context, addr string) (net.Conn, error) {
	attempt := d.race.attempt(addr)
	attempt.start()
	c, err := d.Dialer.Dial(ctx, addr)
	attempt.finish(err == nil)
	return c, err
}

type tcpFallback struct {
	torrent.Dialer
	race *dialRace
	wait time.Duration
}

// Dial waits until uTP to the same peer has failed, or for wait, and then
// dials unless uTP connected.
func (d tcpFallback) Dial(ctx context.Context, addr string) (net.Conn, error) {
	attempt := d.race.attempt(addr)
	timer := time.NewTimer(d.wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-attempt.done:
	case <-timer.C:
	}
	if attempt.connected() {
		return nil, errUTPConnected
	}
	return d.Dialer.Dial(ctx, addr)
}

// dialRace follows the uTP dials to each peer address, so that the TCP dial
// to the same address knows when to give up or go ahead. The library's dial
// context lives on through the handshake, so without this a TCP dial would
// still connect after uTP won, only to be closed again.
type dialRace struct {
	mu       sync.Mutex
	attempts map[string]*dialAttempt
}

// An attempt is fresh for this long; dials to the same address after that
// are a new attempt.
const attemptTTL = 30 * time.Second

type dialAttempt struct {
	created time.Time
	done    chan struct{} // closed when uTP connected or all its dials failed

	mu      sync.Mutex
	pending int
	won     bool
	closed  bool
}

func (r *dialRace) attempt(addr string) *dialAttempt {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if a, ok := r.attempts[addr]; ok && now.Sub(a.created) < attemptTTL {
		return a
	}
	if r.attempts == nil {
		r.attempts = map[string]*dialAttempt{}
	}
	if len(r.attempts) > 256 {
		for k, a := range r.attempts {
			if now.Sub(a.created) >= attemptTTL {
				delete(r.attempts, k)
			}
		}
	}
	a := &dialAttempt{created: now, done: make(chan struct{})}
	r.attempts[addr] = a
	return a
}

func (a *dialAttempt) start() {
	a.mu.Lock()
	a.pending++
	a.mu.Unlock()
}

// finish records one uTP dial's result. The attempt is decided when one
// connects, or when every one started has failed.
func (a *dialAttempt) finish(ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending--
	a.won = a.won || ok
	if !a.closed && (ok || a.pending == 0) {
		a.closed = true
		close(a.done)
	}
}

func (a *dialAttempt) connected() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.won
}

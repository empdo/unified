package engine

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type fakeDialer struct {
	network string
	delay   time.Duration
	err     error
	dialed  chan string
}

func (d fakeDialer) DialerNetwork() string { return d.network }

func (d fakeDialer) Dial(ctx context.Context, addr string) (net.Conn, error) {
	time.Sleep(d.delay)
	if d.dialed != nil {
		d.dialed <- addr
	}
	if d.err != nil {
		return nil, d.err
	}
	a, b := net.Pipe()
	b.Close()
	return a, nil
}

// dialBoth dials like the library does: every dialer at once.
func dialBoth(utp utpDialer, tcp tcpFallback, addr string) (utpErr, tcpErr error) {
	ctx := context.Background()
	res := make(chan error, 1)
	go func() {
		c, err := tcp.Dial(ctx, addr)
		if c != nil {
			c.Close()
		}
		res <- err
	}()
	c, utpErr := utp.Dial(ctx, addr)
	if c != nil {
		c.Close()
	}
	return utpErr, <-res
}

func TestTCPWaitsForUTP(t *testing.T) {
	race := &dialRace{}
	dialed := make(chan string, 1)
	utp := utpDialer{fakeDialer{network: "udp4", delay: 20 * time.Millisecond}, race}
	tcp := tcpFallback{fakeDialer{network: "tcp4", dialed: dialed}, race, time.Second}

	utpErr, tcpErr := dialBoth(utp, tcp, "10.0.0.1:6881")
	if utpErr != nil || !errors.Is(tcpErr, errUTPConnected) {
		t.Fatalf("uTP %v, TCP %v: want uTP to win and TCP to stand down", utpErr, tcpErr)
	}
	if len(dialed) != 0 {
		t.Error("TCP dialed although uTP connected")
	}
}

func TestTCPGoesAheadWhenUTPFails(t *testing.T) {
	race := &dialRace{}
	utp := utpDialer{fakeDialer{network: "udp4", err: errors.New("no uTP")}, race}
	tcp := tcpFallback{fakeDialer{network: "tcp4"}, race, time.Minute}

	start := time.Now()
	utpErr, tcpErr := dialBoth(utp, tcp, "10.0.0.2:6881")
	if utpErr == nil || tcpErr != nil {
		t.Fatalf("uTP %v, TCP %v: want TCP to connect", utpErr, tcpErr)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("TCP waited out the head start although uTP had failed")
	}
}

func TestTCPGoesAheadAfterHeadStart(t *testing.T) {
	race := &dialRace{}
	utp := utpDialer{fakeDialer{network: "udp4", delay: 300 * time.Millisecond, err: errors.New("timeout")}, race}
	tcp := tcpFallback{fakeDialer{network: "tcp4"}, race, 50 * time.Millisecond}
	if _, tcpErr := dialBoth(utp, tcp, "10.0.0.3:6881"); tcpErr != nil {
		t.Fatalf("TCP %v: want it to connect once the head start is over", tcpErr)
	}
}

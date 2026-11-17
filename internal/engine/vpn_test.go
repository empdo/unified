package engine

import (
	"net"
	"testing"
)

func TestBindingCheck(t *testing.T) {
	b, err := bindInterface("lo")
	if err != nil {
		t.Fatal(err)
	}
	if !b.v4.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("v4 = %v, want 127.0.0.1", b.v4)
	}
	if err := b.check(); err != nil {
		t.Errorf("check on an unchanged interface: %v", err)
	}

	changed := *b
	changed.v4 = net.IPv4(10, 64, 0, 2)
	if changed.check() == nil {
		t.Error("check did not notice a changed address")
	}

	gone := binding{iface: "unified-test-missing0", v4: b.v4}
	if gone.check() == nil {
		t.Error("check did not notice a missing interface")
	}
}

func TestBindingLocal(t *testing.T) {
	v4only := binding{v4: net.IPv4(10, 0, 0, 1)}
	for network, want := range map[string]net.IP{"tcp4": v4only.v4, "udp": v4only.v4, "tcp": v4only.v4, "tcp6": nil} {
		if got := v4only.local(network); !got.Equal(want) {
			t.Errorf("local(%q) = %v, want %v", network, got, want)
		}
	}
}

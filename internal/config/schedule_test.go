package config

import (
	"testing"
	"time"
)

func TestWindow(t *testing.T) {
	at := func(hm string) time.Time {
		v, _ := time.Parse("15:04", hm)
		return time.Date(2026, 11, 18, v.Hour(), v.Minute(), 0, 0, time.Local)
	}
	for _, c := range []struct {
		window string
		in     []string
		out    []string
	}{
		{"08:00-23:00", []string{"08:00", "12:30", "22:59"}, []string{"07:59", "23:00", "02:00"}},
		{" 22:00 - 06:00 ", []string{"22:00", "23:59", "00:00", "05:59"}, []string{"06:00", "12:00", "21:59"}},
	} {
		w, ok, err := ParseWindow(c.window)
		if err != nil || !ok {
			t.Fatalf("ParseWindow(%q) = %v, %v", c.window, ok, err)
		}
		for _, hm := range c.in {
			if !w.Contains(at(hm)) {
				t.Errorf("%q does not contain %s", c.window, hm)
			}
		}
		for _, hm := range c.out {
			if w.Contains(at(hm)) {
				t.Errorf("%q contains %s", c.window, hm)
			}
		}
	}

	if _, ok, err := ParseWindow(""); ok || err != nil {
		t.Errorf("empty window: %v, %v", ok, err)
	}
	for _, bad := range []string{"8-23", "08:00", "08:00-25:00", "08:00-08:00", "noon-midnight"} {
		if _, _, err := ParseWindow(bad); err == nil {
			t.Errorf("ParseWindow(%q) gave no error", bad)
		}
	}
}

package config

import (
	"fmt"
	"strings"
	"time"
)

// Window is a daily time window such as "08:00-23:00". One that ends before
// it starts runs past midnight: "22:00-06:00".
type Window struct {
	start, end int // minutes after midnight
}

// ParseWindow reads "HH:MM-HH:MM". An empty string is no window.
func ParseWindow(s string) (w Window, ok bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Window{}, false, nil
	}
	from, to, found := strings.Cut(s, "-")
	if !found {
		return Window{}, false, fmt.Errorf("time window %q: want HH:MM-HH:MM", s)
	}
	if w.start, err = minutes(from); err != nil {
		return Window{}, false, fmt.Errorf("time window %q: %w", s, err)
	}
	if w.end, err = minutes(to); err != nil {
		return Window{}, false, fmt.Errorf("time window %q: %w", s, err)
	}
	if w.start == w.end {
		return Window{}, false, fmt.Errorf("time window %q starts and ends at the same time", s)
	}
	return w, true, nil
}

func minutes(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("%q is not a time like 08:00", strings.TrimSpace(s))
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Contains reports whether t's time of day falls in the window, start
// included and end excluded.
func (w Window) Contains(t time.Time) bool {
	m := t.Hour()*60 + t.Minute()
	if w.start < w.end {
		return m >= w.start && m < w.end
	}
	return m >= w.start || m < w.end
}

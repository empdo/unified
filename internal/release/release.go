// Package release parses release names and decides which release to grab.
package release

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/moistari/rls"

	"unified/internal/config"
	"unified/internal/torznab"
)

// Info is the structured form of a release name such as
// "The.Show.S02E05.1080p.WEB-DL.x265-GROUP".
type Info struct {
	Title      string
	Year       int
	Season     int
	Episode    int // 0 with a Season set means a whole-season pack
	Resolution string
	Source     string
	Codec      string
	Group      string
}

func Parse(name string) Info {
	r := rls.ParseString(name)
	return Info{
		Title:      r.Title,
		Year:       r.Year,
		Season:     r.Series,
		Episode:    r.Episode,
		Resolution: r.Resolution,
		Source:     r.Source,
		Codec:      strings.Join(r.Codec, " "),
		Group:      r.Group,
	}
}

// Want describes what we are looking for. For a show, Episode 0 asks for a
// season pack covering Count episodes.
type Want struct {
	Title   string
	Year    int
	Show    bool
	Season  int
	Episode int
	Count   int
}

// Query is the text sent to the indexers.
func (w Want) Query() string {
	switch {
	case !w.Show:
		return fmt.Sprintf("%s %d", w.Title, w.Year)
	case w.Episode == 0:
		return fmt.Sprintf("%s S%02d", w.Title, w.Season)
	default:
		return fmt.Sprintf("%s S%02dE%02d", w.Title, w.Season, w.Episode)
	}
}

type Candidate struct {
	torznab.Release
	Info Info
	// Reject is empty for acceptable releases, otherwise the reason.
	Reject string
	score  int
}

// Rank parses and judges every release. Acceptable ones come first, best
// first; rejected ones follow so they can be shown with their reason.
func Rank(q config.Quality, w Want, rels []torznab.Release, blocked func(name string) bool) []Candidate {
	seen := map[string]bool{}
	var out []Candidate
	for _, r := range rels {
		if seen[r.Title] {
			continue
		}
		seen[r.Title] = true
		c := Candidate{Release: r, Info: Parse(r.Title)}
		// A title ending in a number, like "Blade Runner 2049", can be parsed
		// as a shorter title and a year when the release has no real year.
		if in := c.Info; in.Year != 0 && normalize(fmt.Sprintf("%s %d", in.Title, in.Year)) == normalize(w.Title) {
			c.Info.Title, c.Info.Year = fmt.Sprintf("%s %d", in.Title, in.Year), 0
		}
		c.Reject, c.score = judge(q, w, c, blocked)
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Reject == "") != (out[j].Reject == "") {
			return out[i].Reject == ""
		}
		return out[i].score > out[j].score
	})
	return out
}

func judge(q config.Quality, w Want, c Candidate, blocked func(string) bool) (reject string, score int) {
	in := c.Info
	if !sameTitle(w, in) {
		return fmt.Sprintf("title %q does not match", in.Title), 0
	}
	if w.Show {
		if in.Season != w.Season || in.Episode != w.Episode {
			return "wrong season/episode", 0
		}
	} else {
		if in.Season != 0 || in.Episode != 0 {
			return "is a TV release", 0
		}
		if in.Year != 0 && (in.Year < w.Year-1 || in.Year > w.Year+1) {
			return fmt.Sprintf("year %d does not match", in.Year), 0
		}
	}

	rank := -1
	for i, res := range q.Resolutions {
		if strings.EqualFold(res, in.Resolution) {
			rank = len(q.Resolutions) - i
		}
	}
	if rank < 0 {
		if in.Resolution == "" {
			return "unknown resolution", 0
		}
		return "resolution " + in.Resolution + " not accepted", 0
	}

	limit := q.MaxMovieGB
	if w.Show {
		limit = q.MaxEpisodeGB * float64(max(w.Count, 1))
	}
	if gb := float64(c.Size) / (1 << 30); limit > 0 && gb > limit {
		return fmt.Sprintf("too large (%.1f GB > %.1f GB)", gb, limit), 0
	}
	if c.Seeders >= 0 && c.Seeders < q.MinSeeders {
		return fmt.Sprintf("only %d seeders", c.Seeders), 0
	}
	if blocked != nil && blocked(c.Title) {
		return "blocklisted", 0
	}

	// Resolution dominates, then preferred codec, then seeders.
	score = rank * 1000
	for _, codec := range q.PreferCodecs {
		if strings.Contains(strings.ToLower(in.Codec), strings.ToLower(codec)) {
			score += 500
			break
		}
	}
	score += min(max(c.Seeders, 0), 499)
	return "", score
}

// sameTitle compares titles ignoring case and punctuation. Releases often
// carry the year as part of the title ("Doctor Who 2005"), so both forms count.
func sameTitle(w Want, in Info) bool {
	got := normalize(in.Title)
	if got == "" {
		return false
	}
	want := normalize(w.Title)
	wantYear := normalize(fmt.Sprintf("%s %d", w.Title, w.Year))
	gotYear := normalize(fmt.Sprintf("%s %d", in.Title, in.Year))
	return got == want || got == wantYear || gotYear == wantYear
}

func normalize(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "&", "and")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

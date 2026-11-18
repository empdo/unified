package release

import (
	"strings"
	"testing"

	"unified/internal/config"
	"unified/internal/torznab"
)

const gb = 1 << 30

var quality = config.Quality{
	Resolutions:  []string{"1080p", "720p"},
	PreferCodecs: []string{"x265"},
	MaxMovieGB:   20,
	MaxEpisodeGB: 5,
	MinSeeders:   2,
}

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		want Info
	}{
		{"The.Show.S02E05.1080p.WEB-DL.x265-GROUP",
			Info{Title: "The Show", Season: 2, Episode: 5, Resolution: "1080p", Source: "WEB-DL", Codec: "x265", Group: "GROUP"}},
		{"Severance.S02.720p.WEB-DL.x264-GRP",
			Info{Title: "Severance", Season: 2, Resolution: "720p", Source: "WEB-DL", Codec: "x264", Group: "GRP"}},
		{"Blade.Runner.2049.2017.1080p.BluRay.x264-GRP",
			Info{Title: "Blade Runner 2049", Year: 2017, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "GRP"}},
	}
	for _, tt := range tests {
		if got := Parse(tt.name); got != tt.want {
			t.Errorf("Parse(%q)\n got %+v\nwant %+v", tt.name, got, tt.want)
		}
	}
}

func TestQuery(t *testing.T) {
	tests := []struct {
		w    Want
		want string
	}{
		{Want{Title: "Blade Runner 2049", Year: 2017}, "Blade Runner 2049 2017"},
		{Want{Title: "Severance", Show: true, Season: 2}, "Severance S02"},
		{Want{Title: "Severance", Show: true, Season: 2, Episode: 5}, "Severance S02E05"},
		{Want{Title: "Frieren: Beyond Journey's End", Show: true, Season: 1, Episode: 5}, "Frieren: Beyond Journeys End S01E05"},
	}
	for _, tt := range tests {
		if got := tt.w.Query(); got != tt.want {
			t.Errorf("%+v.Query() = %q, want %q", tt.w, got, tt.want)
		}
	}
}

func TestQueriesAndAliases(t *testing.T) {
	w := Want{
		Title: "Frieren: Beyond Journey's End", Aliases: []string{"Frieren Beyond Journeys End", "Sousou no Frieren"},
		Show: true, Season: 2, Episode: 5,
	}
	got := w.Queries()
	if len(got) != 2 || got[1] != "Sousou no Frieren S02E05" {
		t.Errorf("Queries() = %q, want the title and the one alias that differs", got)
	}
	q := config.Quality{Resolutions: []string{"1080p"}}
	c := Rank(q, w, []torznab.Release{{Title: "Sousou.no.Frieren.S02E05.1080p.WEB.x264-GRP", Seeders: -1}}, nil)
	if c[0].Reject != "" {
		t.Errorf("release under an alias rejected: %s", c[0].Reject)
	}
}

func TestRankOrder(t *testing.T) {
	w := Want{Title: "Severance", Year: 2022, Show: true, Season: 2, Episode: 5}
	rels := []torznab.Release{
		{Title: "Severance.S02E05.720p.WEB-DL.x265-A", Seeders: 400, Size: gb},
		{Title: "Severance.S02E05.1080p.WEB-DL.x264-B", Seeders: 300, Size: gb},
		{Title: "Severance.S02E06.1080p.WEB-DL.x265-C", Seeders: 999, Size: gb},
		{Title: "Severance.S02E05.1080p.WEB-DL.x265-D", Seeders: 10, Size: gb},
		{Title: "Severance.S02E05.1080p.WEB-DL.x265-E", Seeders: 20, Size: gb},
	}
	got := Rank(quality, w, rels, nil)

	// Resolution beats codec, codec beats seeders, rejected come last.
	want := []string{
		"Severance.S02E05.1080p.WEB-DL.x265-E",
		"Severance.S02E05.1080p.WEB-DL.x265-D",
		"Severance.S02E05.1080p.WEB-DL.x264-B",
		"Severance.S02E05.720p.WEB-DL.x265-A",
		"Severance.S02E06.1080p.WEB-DL.x265-C",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates, want %d", len(got), len(want))
	}
	for i, c := range got {
		if c.Title != want[i] {
			t.Errorf("position %d = %q (%s), want %q", i, c.Title, c.Reject, want[i])
		}
	}
	if got[4].Reject == "" {
		t.Errorf("wrong episode was accepted")
	}
}

func TestRankDeduplicates(t *testing.T) {
	w := Want{Title: "Severance", Year: 2022, Show: true, Season: 2, Episode: 5}
	r := torznab.Release{Title: "Severance.S02E05.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: gb}
	if got := Rank(quality, w, []torznab.Release{r, r}, nil); len(got) != 1 {
		t.Errorf("got %d candidates for one release listed twice, want 1", len(got))
	}
}

func TestReject(t *testing.T) {
	movie := Want{Title: "Blade Runner 2049", Year: 2017}
	episode := Want{Title: "Severance", Year: 2022, Show: true, Season: 2, Episode: 5}
	pack := Want{Title: "Severance", Year: 2022, Show: true, Season: 2, Count: 10}

	tests := []struct {
		desc    string
		w       Want
		rel     torznab.Release
		blocked bool
		reject  string // substring of the reason; "" means accepted
	}{
		{"good movie", movie, torznab.Release{Title: "Blade.Runner.2049.2017.1080p.BluRay.x265-GRP", Seeders: 10, Size: 10 * gb}, false, ""},
		{"number in title taken for a year", movie, torznab.Release{Title: "Blade.Runner.2049.1080p.BluRay.x265-GRP", Seeders: 10, Size: gb}, false, ""},
		{"year off by one is fine", movie, torznab.Release{Title: "Blade.Runner.2049.2018.1080p.BluRay.x265-GRP", Seeders: 10, Size: gb}, false, ""},
		{"year off by two", movie, torznab.Release{Title: "Blade.Runner.2049.2019.1080p.BluRay.x265-GRP", Seeders: 10, Size: gb}, false, "year"},
		{"other title", movie, torznab.Release{Title: "Blade.Runner.1982.1080p.BluRay.x265-GRP", Seeders: 10, Size: gb}, false, "title"},
		{"tv release for a movie", Want{Title: "Fargo", Year: 1996}, torznab.Release{Title: "Fargo.S01E01.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: gb}, false, "TV release"},
		{"resolution not accepted", movie, torznab.Release{Title: "Blade.Runner.2049.2017.2160p.BluRay.x265-GRP", Seeders: 10, Size: gb}, false, "resolution 2160p"},
		{"unknown resolution", movie, torznab.Release{Title: "Blade.Runner.2049.2017.BluRay.x265-GRP", Seeders: 10, Size: gb}, false, "unknown resolution"},
		{"movie too large", movie, torznab.Release{Title: "Blade.Runner.2049.2017.1080p.BluRay.x265-GRP", Seeders: 10, Size: 21 * gb}, false, "too large"},
		{"too few seeders", movie, torznab.Release{Title: "Blade.Runner.2049.2017.1080p.BluRay.x265-GRP", Seeders: 1, Size: gb}, false, "seeders"},
		{"unknown seeders are allowed", movie, torznab.Release{Title: "Blade.Runner.2049.2017.1080p.BluRay.x265-GRP", Seeders: -1, Size: gb}, false, ""},
		{"blocklisted", movie, torznab.Release{Title: "Blade.Runner.2049.2017.1080p.BluRay.x265-GRP", Seeders: 10, Size: gb}, true, "blocklisted"},

		{"good episode", episode, torznab.Release{Title: "Severance.S02E05.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: gb}, false, ""},
		{"title with year", episode, torznab.Release{Title: "Severance.2022.S02E05.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: gb}, false, ""},
		{"punctuation and case ignored", Want{Title: "Law & Order", Year: 1990, Show: true, Season: 1, Episode: 1},
			torznab.Release{Title: "law.and.order.S01E01.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: gb}, false, ""},
		{"wrong episode", episode, torznab.Release{Title: "Severance.S02E06.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: gb}, false, "season/episode"},
		{"pack when episode wanted", episode, torznab.Release{Title: "Severance.S02.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: gb}, false, "season/episode"},
		{"episode too large", episode, torznab.Release{Title: "Severance.S02E05.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: 6 * gb}, false, "too large"},

		{"pack within limit", pack, torznab.Release{Title: "Severance.S02.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: 45 * gb}, false, ""},
		{"pack too large", pack, torznab.Release{Title: "Severance.S02.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: 51 * gb}, false, "too large"},
		{"episode when pack wanted", pack, torznab.Release{Title: "Severance.S02E05.1080p.WEB-DL.x265-GRP", Seeders: 10, Size: gb}, false, "season/episode"},
	}
	for _, tt := range tests {
		blocked := func(string) bool { return tt.blocked }
		got := Rank(quality, tt.w, []torznab.Release{tt.rel}, blocked)[0]
		switch {
		case tt.reject == "" && got.Reject != "":
			t.Errorf("%s: rejected (%s), want accepted", tt.desc, got.Reject)
		case tt.reject != "" && !strings.Contains(got.Reject, tt.reject):
			t.Errorf("%s: reject = %q, want it to mention %q", tt.desc, got.Reject, tt.reject)
		}
	}
}

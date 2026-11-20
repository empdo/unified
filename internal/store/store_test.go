package store

import "testing"

func open(t *testing.T) *Store {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestHistory(t *testing.T) {
	st := open(t)
	for _, e := range []string{EventAdded, EventGrabbed, EventImported} {
		if err := st.AddEvent(e, 1, "Film (2020)", e+" detail"); err != nil {
			t.Fatal(err)
		}
	}
	st.AddEvent(EventAdded, 2, "Other", "")

	all, err := st.History(HistoryQuery{Limit: 10})
	if err != nil || len(all) != 4 || all[0].Title != "Other" {
		t.Fatalf("all: %+v, %v", all, err)
	}
	film, _ := st.History(HistoryQuery{MediaID: 1, Events: []string{EventGrabbed, EventImported}, Limit: 10})
	if len(film) != 2 || film[0].Event != EventImported || film[1].Event != EventGrabbed {
		t.Errorf("filtered: %+v", film)
	}
	older, _ := st.History(HistoryQuery{Before: all[1].ID, Limit: 1})
	if len(older) != 1 || older[0].ID != all[2].ID {
		t.Errorf("paging: %+v", older)
	}
}

func TestCounters(t *testing.T) {
	st := open(t)
	st.AddCounters(map[string]int64{TotalDownloaded: 100, TotalUploaded: 5})
	st.AddCounters(map[string]int64{TotalDownloaded: 50})
	if d, u := st.Counter(TotalDownloaded), st.Counter(TotalUploaded); d != 150 || u != 5 {
		t.Errorf("got %d, %d", d, u)
	}
}

func TestSeeding(t *testing.T) {
	st := open(t)
	id, _ := st.AddMedia(Media{Kind: "movie", TMDBID: 1, Title: "Film", Year: 2020})
	st.UpsertItem(id, 0, 0, "Film", "", true)
	items, _ := st.Items(id)
	dl, err := st.CreateDownload("abc", "Film.2020.1080p", "magnet:?xt=urn:btih:abc", []int64{items[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	st.SetImported(items[0].ID, "/movies/Film (2020)/Film (2020).mkv")
	st.FinishDownload(dl.ID)
	st.SetSeeding(dl.ID, true)
	st.SetTransferred(dl.ID, 1000, 250)
	st.SetSource(dl.ID, "/data/torrents/abc.torrent")

	seeds, err := st.SeedingDownloads()
	if err != nil || len(seeds) != 1 {
		t.Fatalf("seeds: %+v, %v", seeds, err)
	}
	s := seeds[0]
	if s.Downloaded != 1000 || s.Uploaded != 250 || s.Source != "/data/torrents/abc.torrent" || s.FinishedAt.IsZero() {
		t.Errorf("seed: %+v", s)
	}
	if got := st.DownloadMedia(dl.ID); got != id {
		t.Errorf("DownloadMedia = %d, want %d", got, id)
	}
	st.SetSeeding(dl.ID, false)
	if seeds, _ := st.SeedingDownloads(); len(seeds) != 0 {
		t.Errorf("still seeding: %+v", seeds)
	}
}

func TestListSeen(t *testing.T) {
	st := open(t)
	id, err := st.AddList(List{Kind: TraktWatchlist, Source: "someone", Name: "someone's watchlist", Want: WantAll})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddList(List{Kind: TraktWatchlist, Source: "someone", Want: WantAll}); err == nil {
		t.Error("the same list was added twice")
	}
	st.MarkSeen(id, "movie", 7)
	if !st.Seen(id, "movie", 7) || st.Seen(id, "show", 7) {
		t.Error("seen is wrong")
	}
	st.RemoveList(id)
	if st.Seen(id, "movie", 7) {
		t.Error("seen entries outlived their list")
	}
}

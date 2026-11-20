package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"unified/internal/config"
	"unified/internal/engine"
	"unified/internal/store"
)

// seedRunner has a finished download of a movie that was still seeding when
// unified stopped, with its file in the download directory.
func seedRunner(t *testing.T) (*config.Config, *store.Store, store.Download, string) {
	dir := t.TempDir()
	cfg := &config.Config{
		DataDir: filepath.Join(dir, "data"), DownloadDir: filepath.Join(dir, "dl"),
		MoviesDir: filepath.Join(dir, "movies"), TVDir: filepath.Join(dir, "tv"), SeedRatio: 1,
	}
	file := filepath.Join(cfg.DownloadDir, "Film.2020.1080p.mkv")
	os.MkdirAll(cfg.DownloadDir, 0o755)
	if err := os.WriteFile(file, make([]byte, 100<<10), 0o644); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 16 << 10}
	if err := info.BuildFromFilePath(file); err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{}
	var err error
	if mi.InfoBytes, err = bencode.Marshal(info); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(cfg.DataDir, 0o755)
	source := filepath.Join(cfg.DataDir, "film.torrent")
	f, _ := os.Create(source)
	mi.Write(f)
	f.Close()

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	id, _ := st.AddMedia(store.Media{Kind: "movie", TMDBID: 1, Title: "Film", Year: 2020})
	st.UpsertItem(id, 0, 0, "Film", "", true)
	items, _ := st.Items(id)
	dl, err := st.CreateDownload(mi.HashInfoBytes().HexString(), "Film.2020.1080p", source, []int64{items[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	st.SetImported(items[0].ID, filepath.Join(cfg.MoviesDir, "Film (2020)", "Film (2020).mkv"))
	st.FinishDownload(dl.ID)
	st.SetSeeding(dl.ID, true)
	// Half its size uploaded before the restart.
	st.SetTransferred(dl.ID, 100<<10, 50<<10)
	return cfg, st, dl, file
}

func newSeedRunner(t *testing.T, cfg *config.Config, st *store.Store) *runner {
	eng, err := engine.New(cfg.DownloadDir, filepath.Join(cfg.DataDir, "torrents"), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return &runner{
		ctx: context.Background(), cfg: func() *config.Config { return cfg }, st: st, eng: eng,
		active: map[int64]*activeDownload{}, seeding: map[int64]*activeDownload{},
	}
}

func TestResumeSeeds(t *testing.T) {
	cfg, st, dl, _ := seedRunner(t)
	r := newSeedRunner(t, cfg, st)
	if err := r.resumeSeeds(); err != nil {
		t.Fatal(err)
	}
	a := r.seeding[dl.ID]
	if a == nil {
		t.Fatal("not seeding after a restart")
	}
	if a.mediaID == 0 || a.seedSince.IsZero() {
		t.Errorf("seed: media %d, since %v", a.mediaID, a.seedSince)
	}
	<-a.t.GotInfo()
	// Its ratio counts what it uploaded before the restart.
	if ratio := a.ratio(); ratio < 0.49 || ratio > 0.51 {
		t.Errorf("ratio %.2f, want 0.5", ratio)
	}
	r.stopSeeding()
	if r.seeding[dl.ID] == nil {
		t.Fatal("stopped seeding with its file in place")
	}
	if seeds, _ := st.SeedingDownloads(); len(seeds) != 1 {
		t.Errorf("seeding in the store: %+v", seeds)
	}
}

func TestResumeSeedsWithoutFiles(t *testing.T) {
	cfg, st, dl, file := seedRunner(t)
	os.Remove(file)
	r := newSeedRunner(t, cfg, st)
	if err := r.resumeSeeds(); err != nil {
		t.Fatal(err)
	}
	a := r.seeding[dl.ID]
	if a == nil {
		t.Fatal("not added back")
	}
	select {
	case <-a.t.GotInfo():
	case <-time.After(5 * time.Second):
		t.Fatal("no info from a .torrent file")
	}
	r.stopSeeding()
	if r.seeding[dl.ID] != nil {
		t.Error("still seeding without its file")
	}
	if seeds, _ := st.SeedingDownloads(); len(seeds) != 0 {
		t.Errorf("seeding again on the next start: %+v", seeds)
	}
}

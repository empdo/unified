// unified finds, downloads and files movies and TV episodes in one program.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"unified/internal/config"
	"unified/internal/engine"
	"unified/internal/release"
	"unified/internal/store"
	"unified/internal/tmdb"
	"unified/internal/torznab"
)

const usage = `usage: unified <command>

  init                              write a starter config file
  serve [--addr host:port]          run continuously with a web UI (default 127.0.0.1:8080)

  add movie <title>                 add a movie to the library
  add show <title> [-s N] [-e N]    add a show; limit to a season or one episode
  list [media-id]                   show the library, or one show's episodes
  rm <media-id>                     remove from the library (files are kept)
  search <media-id> [season [ep]]   show how releases are ranked, without grabbing
  run [--seed]                      grab, download and import everything wanted
  downloads                         list downloads
  fail <download-id>                blocklist a download and look for another release

  parse <release name>              show how a release name is parsed
  get <magnet or .torrent url>      download a single torrent, nothing else

config: ` + "%s\n"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usage, config.DefaultPath())
		os.Exit(2)
	}
	if err := dispatch(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func dispatch(cmd string, args []string) error {
	// Commands that need no config.
	switch cmd {
	case "init":
		path := config.DefaultPath()
		if err := config.WriteTemplate(path); err != nil {
			return err
		}
		fmt.Println("wrote", path, "- add your TMDB key and an indexer, then try `unified add movie <title>`")
		return nil
	case "parse":
		if len(args) == 0 {
			return fmt.Errorf("usage: unified parse <release name>")
		}
		fmt.Printf("%+v\n", release.Parse(strings.Join(args, " ")))
		return nil
	case "help", "-h", "--help":
		fmt.Printf(usage, config.DefaultPath())
		return nil
	}

	cfg, err := config.Load(config.DefaultPath())
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		return cmdServe(ctx, st, args)
	case "add":
		return cmdAdd(cfg, st, args)
	case "list":
		return cmdList(st, args)
	case "rm":
		id, err := idArg(args)
		if err != nil {
			return err
		}
		return st.RemoveMedia(id)
	case "search":
		return cmdSearch(ctx, cfg, st, args)
	case "run":
		return cmdRun(ctx, cfg, st, len(args) > 0 && args[0] == "--seed")
	case "downloads":
		dls, err := st.Downloads(false)
		for _, d := range dls {
			fmt.Printf("[%d] %-11s %s\n", d.ID, d.State, d.Name)
		}
		return err
	case "fail":
		id, err := idArg(args)
		if err != nil {
			return err
		}
		return st.FailDownload(id)
	case "get":
		if len(args) != 1 {
			return fmt.Errorf("usage: unified get <magnet or .torrent url>")
		}
		return cmdGet(ctx, cfg, args[0])
	}
	return fmt.Errorf("unknown command %q, try `unified help`", cmd)
}

func idArg(args []string) (int64, error) {
	if len(args) == 0 {
		return 0, fmt.Errorf("missing id")
	}
	return strconv.ParseInt(args[0], 10, 64)
}

func cmdAdd(cfg *config.Config, st *store.Store, args []string) error {
	if len(args) < 2 || (args[0] != "movie" && args[0] != "show") {
		return fmt.Errorf("usage: unified add movie|show <title> [-s season] [-e episode]")
	}
	kind := args[0]
	season, episode := 0, 0
	var words []string
	for i := 1; i < len(args); i++ {
		if (args[i] == "-s" || args[i] == "-e") && i+1 < len(args) {
			n, err := strconv.Atoi(args[i+1])
			if err != nil {
				return fmt.Errorf("%s needs a number", args[i])
			}
			if args[i] == "-s" {
				season = n
			} else {
				episode = n
			}
			i++
			continue
		}
		words = append(words, args[i])
	}
	if episode != 0 && season == 0 {
		return fmt.Errorf("-e needs -s")
	}

	results, err := tmdb.New(cfg.TMDBKey).Search(kind, strings.Join(words, " "))
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("nothing found on TMDB")
	}
	pick := results[0]
	if len(results) > 1 {
		results = results[:min(len(results), 8)]
		for i, r := range results {
			fmt.Printf("  %d. %s (%d)\n", i+1, r.Title, r.Year)
		}
		fmt.Print("which one? [1] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if line = strings.TrimSpace(line); line != "" {
			n, err := strconv.Atoi(line)
			if err != nil || n < 1 || n > len(results) {
				return fmt.Errorf("not one of the choices")
			}
			pick = results[n-1]
		}
	}

	want := func(s, e int) bool {
		return (season == 0 || s == season) && (episode == 0 || e == episode)
	}
	id, total, wanted, err := addToLibrary(cfg, st, kind, pick, want)
	if err != nil {
		return err
	}
	if kind == "movie" {
		fmt.Printf("added [%d] %s (%d)\n", id, pick.Title, pick.Year)
	} else {
		fmt.Printf("added [%d] %s (%d): %d episodes, %d wanted\n", id, pick.Title, pick.Year, total, wanted)
	}
	return nil
}

// addToLibrary stores a TMDB result and, for a show, its episodes. want
// decides which items are wanted; a movie is asked about season 0, episode 0.
func addToLibrary(cfg *config.Config, st *store.Store, kind string, pick tmdb.Result, want func(season, episode int) bool) (id int64, total, wanted int, err error) {
	id, err = st.AddMedia(store.Media{
		Kind: kind, TMDBID: pick.ID, Title: pick.Title, Year: pick.Year,
		Poster: pick.Poster, Overview: pick.Overview,
	})
	if err != nil {
		return 0, 0, 0, err
	}
	if kind == "movie" {
		w := want(0, 0)
		if w {
			wanted = 1
		}
		return id, 1, wanted, st.UpsertItem(id, 0, 0, pick.Title, "", w)
	}
	episodes, err := tmdb.New(cfg.TMDBKey).Episodes(pick.ID)
	if err != nil {
		return id, 0, 0, err
	}
	for _, e := range episodes {
		w := want(e.Season, e.Number)
		if w {
			wanted++
		}
		if err := st.UpsertItem(id, e.Season, e.Number, e.Title, e.AirDate, w); err != nil {
			return id, 0, 0, err
		}
	}
	return id, len(episodes), wanted, nil
}

func cmdList(st *store.Store, args []string) error {
	if len(args) > 0 {
		id, err := idArg(args)
		if err != nil {
			return err
		}
		items, err := st.Items(id)
		for _, it := range items {
			fmt.Printf("S%02dE%02d  %-11s %-10s %s\n", it.Season, it.Episode, it.State, it.AirDate, it.Title)
		}
		return err
	}
	media, err := st.AllMedia()
	if err != nil {
		return err
	}
	for _, m := range media {
		s, err := summary(st, m)
		if err != nil {
			return err
		}
		fmt.Printf("[%d] %-5s %s (%d): %s\n", m.ID, m.Kind, m.Title, m.Year, s)
	}
	return nil
}

// summary describes the state of a movie ("wanted") or a show
// ("3 wanted, 7 imported").
func summary(st *store.Store, m store.Media) (string, error) {
	items, err := st.Items(m.ID)
	if err != nil {
		return "", err
	}
	counts := map[string]int{}
	for _, it := range items {
		counts[it.State]++
	}
	var parts []string
	for _, state := range []string{store.Wanted, store.Downloading, store.Imported, store.Ignored} {
		if n := counts[state]; n > 0 && m.IsShow() {
			parts = append(parts, fmt.Sprintf("%d %s", n, state))
		} else if n > 0 {
			parts = append(parts, state)
		}
	}
	return strings.Join(parts, ", "), nil
}

// wantFor describes what to search for: a movie, one episode, or with
// episode 0 a whole season.
func wantFor(st *store.Store, m store.Media, season, episode int) (release.Want, error) {
	w := release.Want{Title: m.Title, Year: m.Year, Show: m.IsShow(), Count: 1}
	if !m.IsShow() {
		return w, nil
	}
	w.Season, w.Episode = season, episode
	if episode == 0 {
		items, err := st.Items(m.ID)
		if err != nil {
			return w, err
		}
		w.Count = 0
		for _, it := range items {
			if it.Season == season {
				w.Count++
			}
		}
	}
	return w, nil
}

// searchAll queries every configured indexer, reporting failures through
// logf without giving up on the others.
func searchAll(ctx context.Context, cfg *config.Config, w release.Want, logf func(string, ...any)) []torznab.Release {
	category := torznab.Movies
	if w.Show {
		category = torznab.TV
	}
	var all []torznab.Release
	for _, ix := range cfg.Indexers {
		rels, err := torznab.Search(ctx, ix, w.Query(), category)
		if err != nil {
			logf("  indexer error: %v", err)
			continue
		}
		all = append(all, rels...)
	}
	return all
}

func cmdSearch(ctx context.Context, cfg *config.Config, st *store.Store, args []string) error {
	id, err := idArg(args)
	if err != nil {
		return err
	}
	if len(cfg.Indexers) == 0 {
		return fmt.Errorf("no indexers configured")
	}
	m, err := st.Media(id)
	if err != nil {
		return fmt.Errorf("no media with id %d", id)
	}
	season, episode := 0, 0
	if m.IsShow() {
		if len(args) < 2 {
			return fmt.Errorf("usage: unified search <media-id> <season> [episode]")
		}
		season, _ = strconv.Atoi(args[1])
		if len(args) > 2 {
			episode, _ = strconv.Atoi(args[2])
		}
	}
	w, err := wantFor(st, m, season, episode)
	if err != nil {
		return err
	}
	fmt.Printf("searching for %q\n", w.Query())
	stderr := func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	cands := release.Rank(cfg.Quality, w, searchAll(ctx, cfg, w, stderr), st.Blocked)
	for _, c := range cands {
		verdict := "ok"
		if c.Reject != "" {
			verdict = "no: " + c.Reject
		}
		fmt.Printf("%9s %4d seeds  %-70s %s\n", engine.Size(c.Size), c.Seeders, clip(c.Title, 70), verdict)
	}
	if len(cands) == 0 {
		fmt.Println("no results")
	}
	return nil
}

func cmdGet(ctx context.Context, cfg *config.Config, link string) error {
	eng, err := engine.New(cfg.DownloadDir, filepath.Join(cfg.DataDir, "torrents"), cfg.ListenPort, cfg.VPNInterface)
	if err != nil {
		return err
	}
	defer eng.Close()
	source, err := eng.Resolve(ctx, link)
	if err != nil {
		return err
	}
	t, err := eng.Add(source)
	if err != nil {
		return err
	}
	started := false
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if engine.HasInfo(t) && !started {
			fmt.Println("downloading", t.Name())
			t.DownloadAll()
			started = true
		}
		fmt.Println(" ", engine.Status(t))
		if started && t.Complete().Bool() {
			fmt.Println("done:", filepath.Join(cfg.DownloadDir, t.Name()))
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// Package config loads the TOML configuration file.
package config

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/BurntSushi/toml"
)

type Config struct {
	DataDir     string `toml:"data_dir"`
	DownloadDir string `toml:"download_dir"`
	MoviesDir   string `toml:"movies_dir"`
	TVDir       string `toml:"tv_dir"`
	TMDBKey     string `toml:"tmdb_api_key"`
	ListenPort  int    `toml:"listen_port"`
	// Network interface all torrent traffic is bound to, e.g. "wg0-mullvad".
	VPNInterface string `toml:"vpn_interface"`
	// Password for the web UI; empty means no login.
	Password string    `toml:"password"`
	Quality  Quality   `toml:"quality"`
	Indexers []Indexer `toml:"indexer"`
}

type Quality struct {
	// Accepted resolutions, most preferred first.
	Resolutions  []string `toml:"resolutions"`
	PreferCodecs []string `toml:"prefer_codecs"`
	MaxMovieGB   float64  `toml:"max_movie_gb"`
	MaxEpisodeGB float64  `toml:"max_episode_gb"`
	MinSeeders   int      `toml:"min_seeders"`
}

type Indexer struct {
	Name   string `toml:"name"`
	URL    string `toml:"url"`
	APIKey string `toml:"api_key"`
}

const template = `# unified configuration

# Database and saved .torrent files.
data_dir     = "%[1]s"
# Downloads and the library must be on the same filesystem for hardlinks
# to work; otherwise files are copied.
download_dir = "%[2]s/downloads"
movies_dir   = "%[2]s/movies"
tv_dir       = "%[2]s/tv"

# Free key from https://www.themoviedb.org/settings/api
# (either the "API Key" or the "API Read Access Token" works).
tmdb_api_key = ""

listen_port = 42069

# Bind all torrent traffic to this network interface, so it stops instead of
# leaking if the VPN drops. The Mullvad app on Linux uses "wg0-mullvad"; run
# "ip -br addr" while connected to see the name. Leave empty to not bind.
vpn_interface = ""

# Password for the web UI (any user name works). Leave empty for no login,
# e.g. when Home Assistant handles access.
password = ""

[quality]
resolutions    = ["1080p", "720p"]   # most preferred first
prefer_codecs  = ["x265", "HEVC", "H.265"]
max_movie_gb   = 15.0
max_episode_gb = 4.0
min_seeders    = 2

# One block per Torznab indexer, same details as in Sonarr/Radarr.
# For Prowlarr the URL looks like http://localhost:9696/1/api
[[indexer]]
name    = "example"
url     = ""
api_key = ""
`

// DefaultPath returns $UNIFIED_CONFIG, or unified/config.toml in the user config dir.
func DefaultPath() string {
	if p := os.Getenv("UNIFIED_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "unified", "config.toml")
}

// WriteTemplate creates a starter config at path, refusing to overwrite.
// Data goes in $UNIFIED_DATA_DIR and media in $UNIFIED_MEDIA_DIR, by default
// ~/unified/data and ~/unified.
func WriteTemplate(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	data := cmp.Or(os.Getenv("UNIFIED_DATA_DIR"), filepath.Join(home, "unified", "data"))
	media := cmp.Or(os.Getenv("UNIFIED_MEDIA_DIR"), filepath.Join(home, "unified"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(template, data, media)
	return os.WriteFile(path, []byte(body), 0o600)
}

// Load reads the config file and applies environment overrides.
func Load(path string) (*Config, error) {
	c, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	applyEnv(c)
	return c, nil
}

// loadFile reads the config file as written, without environment overrides.
func loadFile(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no config at %s, run `unified init` first", path)
		}
		return nil, err
	}
	if err := c.normalize(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) normalize() error {
	for _, p := range []*string{&c.DataDir, &c.DownloadDir, &c.MoviesDir, &c.TVDir} {
		if *p == "" {
			return fmt.Errorf("data_dir, download_dir, movies_dir and tv_dir must all be set")
		}
		*p = expandHome(*p)
	}
	if len(c.Quality.Resolutions) == 0 {
		c.Quality.Resolutions = []string{"1080p", "720p"}
	}
	// Drop indexer blocks that were left as the empty example.
	var ix []Indexer
	for _, i := range c.Indexers {
		if i.URL != "" {
			ix = append(ix, i)
		}
	}
	c.Indexers = ix
	return nil
}

// The container sets these from its own settings.
var envOverrides = map[string]string{
	"password":      "UNIFIED_PASSWORD",
	"vpn_interface": "UNIFIED_VPN_INTERFACE",
}

func applyEnv(c *Config) {
	if v := os.Getenv(envOverrides["vpn_interface"]); v != "" {
		c.VPNInterface = v
	}
	if v := os.Getenv(envOverrides["password"]); v != "" {
		c.Password = v
	}
}

// FromEnv reports whether a setting ("password" or "vpn_interface") is set
// by an environment variable, which takes precedence over the file.
func FromEnv(setting string) bool {
	return os.Getenv(envOverrides[setting]) != ""
}

func (c *Config) clone() *Config {
	n := *c
	n.Indexers = slices.Clone(c.Indexers)
	n.Quality.Resolutions = slices.Clone(c.Quality.Resolutions)
	n.Quality.PreferCodecs = slices.Clone(c.Quality.PreferCodecs)
	return &n
}

// Live is a config that can change while the program runs. Changes are
// saved to the file; readers see each version whole.
type Live struct {
	path string
	mu   sync.Mutex // serializes Update
	file *Config    // as in the file
	cur  atomic.Pointer[Config]
}

func NewLive(path string) (*Live, error) {
	c, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	l := &Live{path: path, file: c}
	l.publish()
	return l, nil
}

func (l *Live) publish() {
	c := l.file.clone()
	applyEnv(c)
	l.cur.Store(c)
}

// Get returns the current config, including environment overrides. Callers
// must not modify it.
func (l *Live) Get() *Config { return l.cur.Load() }

// File returns a copy of the config as written in the file.
func (l *Live) File() *Config {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.clone()
}

// Update applies change to a copy of the file config, then saves and
// publishes it. Nothing changes if change or saving fails.
func (l *Live) Update(change func(*Config) error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	next := l.file.clone()
	if err := change(next); err != nil {
		return err
	}
	if err := next.normalize(); err != nil {
		return err
	}
	if err := write(l.path, next); err != nil {
		return err
	}
	l.file = next
	l.publish()
	return nil
}

// write replaces the config file. Comments from the original are lost.
func write(path string, c *Config) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	fmt.Fprintln(tmp, "# unified configuration, saved from the settings page.")
	fmt.Fprintln(tmp)
	if err := toml.NewEncoder(tmp).Encode(c); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return p
}

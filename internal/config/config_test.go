package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveUpdateKeepsEnvOutOfFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	os.WriteFile(path, []byte(`data_dir = "/d"
download_dir = "/m/downloads"
movies_dir = "/m/movies"
tv_dir = "/m/tv"
vpn_interface = "from-file"

[[indexer]]
name = "a"
url = "http://a/api"
`), 0o600)
	t.Setenv("UNIFIED_VPN_INTERFACE", "wg0")

	l, err := NewLive(path)
	if err != nil {
		t.Fatal(err)
	}
	if l.Get().VPNInterface != "wg0" || l.File().VPNInterface != "from-file" {
		t.Fatalf("env override: live %q, file %q", l.Get().VPNInterface, l.File().VPNInterface)
	}
	if err := l.Update(func(c *Config) error { c.TMDBKey = "key"; return nil }); err != nil {
		t.Fatal(err)
	}
	written, _ := os.ReadFile(path)
	if !strings.Contains(string(written), `vpn_interface = "from-file"`) || strings.Contains(string(written), "wg0") {
		t.Errorf("saved file has the env value instead of the file value:\n%s", written)
	}
	reread, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reread.TMDBKey != "key" || len(reread.Indexers) != 1 || reread.Indexers[0].URL != "http://a/api" {
		t.Errorf("saved config reads back as %+v", reread)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("saved file mode %v, want 0600", info.Mode().Perm())
	}
}

func TestDefaultsForOldFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`data_dir = "/d"
download_dir = "/m/downloads"
movies_dir = "/m/movies"
tv_dir = "/m/tv"
max_active_seeds = 0
`), 0o600)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// Left out: the defaults. Written as 0: no limit, as written.
	if c.MaxActiveDownloads != 3 || c.MaxConnections != 200 || c.MinFreeGB != DefaultMinFreeGB || c.MaxActiveSeeds != 0 {
		t.Errorf("got %+v", c)
	}
}

func TestTemplateAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UNIFIED_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("UNIFIED_MEDIA_DIR", filepath.Join(dir, "media"))
	path := filepath.Join(dir, "config.toml")
	if err := WriteTemplate(path); err != nil {
		t.Fatal(err)
	}
	l, err := NewLive(path)
	if err != nil {
		t.Fatalf("the template does not load: %v", err)
	}
	c := l.Get()
	if c.MaxActiveDownloads != 3 || c.MaxActiveSeeds != 3 || c.MaxConnections != 200 || c.MaxUploadKBps != 0 || c.AltSchedule != "" {
		t.Errorf("template values: %+v", c)
	}

	err = l.Update(func(c *Config) error {
		c.MaxDownloadKBps, c.MaxUploadKBps = 5000, 800
		c.AltDownloadKBps, c.AltUploadKBps, c.AltSchedule = 2000, 200, "08:00-23:00"
		c.MaxActiveDownloads, c.MaxActiveSeeds, c.MaxConnections = 2, 0, 150
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.MaxDownloadKBps != 5000 || r.MaxUploadKBps != 800 || r.AltDownloadKBps != 2000 || r.AltUploadKBps != 200 ||
		r.AltSchedule != "08:00-23:00" || r.MaxActiveDownloads != 2 || r.MaxActiveSeeds != 0 || r.MaxConnections != 150 {
		t.Errorf("read back %+v", r)
	}

	for _, bad := range []func(*Config){
		func(c *Config) { c.AltSchedule = "evenings" },
		func(c *Config) { c.MaxUploadKBps = -1 },
	} {
		if err := l.Update(func(c *Config) error { bad(c); return nil }); err == nil {
			t.Error("an invalid setting was saved")
		}
	}
}

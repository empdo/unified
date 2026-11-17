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

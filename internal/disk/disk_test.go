package disk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVolumes(t *testing.T) {
	root := t.TempDir()
	vols := Volumes([]Dir{
		{"Downloads", root},
		{"Movies", filepath.Join(root, "movies", "not", "created")},
	})
	if len(vols) != 1 {
		t.Fatalf("got %d volumes, want 1: %+v", len(vols), vols)
	}
	v := vols[0]
	if v.Error != "" || v.Total == 0 || v.Free > v.Total || len(v.Labels) != 2 {
		t.Errorf("unexpected volume %+v", v)
	}
}

func TestMeasure(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, n int) string {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("Small (2020)/Small (2020).mkv", 10_000)
	big := write("Big (2021)/Big (2021).mkv", 100_000)
	write("Big (2021)/Big (2021).en.srt", 5_000)
	if err := os.Link(big, filepath.Join(root, "seeding.mkv")); err != nil {
		t.Fatal(err)
	}

	f, err := Measure(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Files != 4 || len(f.Entries) != 3 || f.Entries[0].Name != "Big (2021)" || f.Entries[0].Size < 105_000 {
		t.Errorf("unexpected result %+v", f)
	}
	if f.Linked < 200_000 {
		t.Errorf("linked = %d, want both links of the big file counted", f.Linked)
	}

	if f, err := Measure(context.Background(), filepath.Join(root, "missing")); err != nil || f.Size != 0 {
		t.Errorf("missing dir: %+v, %v", f, err)
	}
}

package discover

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFindsTools(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "BetterGI", "BetterGI.exe"))
	touch(t, filepath.Join(root, "sub", "ok-ww.exe"))
	touch(t, filepath.Join(root, "M9A", "MaaPiCli.exe"))
	if err := os.MkdirAll(filepath.Join(root, "Fhoe-Rail"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := Scan(context.Background(), Options{Paths: []string{root}})

	byAdapter := map[string][]Candidate{}
	for _, c := range res.Candidates {
		byAdapter[c.Adapter] = append(byAdapter[c.Adapter], c)
	}
	if len(byAdapter["genshin"]) != 1 || byAdapter["genshin"][0].Kind != "exe" {
		t.Errorf("genshin: %+v", byAdapter["genshin"])
	}
	if len(byAdapter["wuwa"]) != 1 {
		t.Errorf("wuwa not found: %+v", res.Candidates)
	}
	if len(byAdapter["r1999"]) != 1 {
		t.Errorf("r1999 not found")
	}
	// Fhoe-Rail matched as a directory.
	var foundDir bool
	for _, c := range byAdapter["hsr"] {
		if c.Kind == "dir" && c.Tool == "Fhoe-Rail" {
			foundDir = true
		}
	}
	if !foundDir {
		t.Errorf("Fhoe-Rail dir not found: %+v", byAdapter["hsr"])
	}
}

func TestScanCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "bettergi.EXE"))
	res := Scan(context.Background(), Options{Paths: []string{root}})
	if len(res.Candidates) != 1 {
		t.Fatalf("case-insensitive match failed: %+v", res.Candidates)
	}
}

func TestScanRespectsMaxDepth(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "d", "e", "BetterGI.exe")
	touch(t, deep)
	res := Scan(context.Background(), Options{Paths: []string{root}, MaxDepth: 2})
	if len(res.Candidates) != 0 {
		t.Errorf("should not find file below max depth: %+v", res.Candidates)
	}
	res = Scan(context.Background(), Options{Paths: []string{root}, MaxDepth: 10})
	if len(res.Candidates) != 1 {
		t.Errorf("should find file within depth: %+v", res.Candidates)
	}
}

func TestScanSkipsNoiseDirs(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "node_modules", "BetterGI.exe"))
	res := Scan(context.Background(), Options{Paths: []string{root}, MaxDepth: 5})
	if len(res.Candidates) != 0 {
		t.Errorf("should skip node_modules: %+v", res.Candidates)
	}
}

func TestScanMissingRoot(t *testing.T) {
	res := Scan(context.Background(), Options{Paths: []string{filepath.Join(t.TempDir(), "nope")}})
	if len(res.Candidates) != 0 {
		t.Errorf("missing root should yield no candidates")
	}
}

func TestDefaultRoots(t *testing.T) {
	if len(DefaultRoots()) == 0 {
		t.Error("expected at least one default root")
	}
}

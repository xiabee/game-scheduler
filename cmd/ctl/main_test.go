package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrettify(t *testing.T) {
	out := prettify([]byte(`{"b":2,"a":1}`))
	if !strings.Contains(out, "\n") || !strings.Contains(out, `"a": 1`) {
		t.Fatalf("unexpected prettify output: %q", out)
	}
}

func TestReadDataSources(t *testing.T) {
	// inline JSON passes through untouched
	b, err := readData(`{"x":1}`)
	if err != nil || string(b) != `{"x":1}` {
		t.Fatalf("inline: %v %q", err, b)
	}
	// @file reads the file content
	dir := t.TempDir()
	path := filepath.Join(dir, "seed.json")
	if err := os.WriteFile(path, []byte(`{"from":"file"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err = readData("@" + path)
	if err != nil || string(b) != `{"from":"file"}` {
		t.Fatalf("@file: %v %q", err, b)
	}
	// @file with a UTF-8 BOM (Notepad / PS 5.1 Set-Content) is stripped
	bomPath := filepath.Join(dir, "bom.json")
	if err := os.WriteFile(bomPath, append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"bom":true}`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err = readData("@" + bomPath)
	if err != nil || string(b) != `{"bom":true}` {
		t.Fatalf("@bom-file: %v %q", err, b)
	}
	// @missing-file is a clear error
	if _, err := readData("@" + filepath.Join(dir, "nope.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
	// bare @ is a clear error
	if _, err := readData("@"); err == nil {
		t.Fatal("expected error for bare @")
	}
	// empty is still an error (existing behavior preserved)
	if _, err := readData(""); err == nil {
		t.Fatal("expected error for empty -data")
	}
}

func TestPlannerPayloadShape(t *testing.T) {
	body := map[string]any{"goal_id": 1, "daily_stamina": 160, "max_tasks": 3}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "goal_id") {
		t.Fatalf("planner payload missing goal_id: %s", b)
	}
}

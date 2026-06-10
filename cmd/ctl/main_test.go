package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrettify(t *testing.T) {
	out := prettify([]byte(`{"b":2,"a":1}`))
	if !strings.Contains(out, "\n") || !strings.Contains(out, `"a": 1`) {
		t.Fatalf("unexpected prettify output: %q", out)
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

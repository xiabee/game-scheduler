package guide

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// Canonical WBI test vector from the bilibili-API-collect documentation.
func TestMixinKeyVector(t *testing.T) {
	got := mixinKey("7cd084941338484aae1ad9425b84077c", "4932caff0ff746eab6f01bf08b70ac45")
	want := "ea1db124af3c7062474693fa704f4ff8"
	if got != want {
		t.Fatalf("mixinKey=%q want %q", got, want)
	}
	if len(got) != 32 {
		t.Fatalf("mixin key must be 32 chars, got %d", len(got))
	}
}

func TestSignParams(t *testing.T) {
	now := time.Unix(1702204169, 0)
	p := url.Values{}
	p.Set("foo", "one one four")
	p.Set("bar", "五一四")
	p.Set("baz", "1919810")
	signed := signParams(p, "ea1db124af3c7062474693fa704f4ff8", now)

	if signed.Get("wts") != "1702204169" {
		t.Errorf("wts=%q", signed.Get("wts"))
	}
	rid := signed.Get("w_rid")
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(rid) {
		t.Fatalf("w_rid not 32-hex: %q", rid)
	}
	// Deterministic: same inputs, same signature.
	p2 := url.Values{}
	p2.Set("foo", "one one four")
	p2.Set("bar", "五一四")
	p2.Set("baz", "1919810")
	if signParams(p2, "ea1db124af3c7062474693fa704f4ff8", now).Get("w_rid") != rid {
		t.Error("signature not deterministic")
	}
	// Filtered characters in values must not change presence of signature.
	p3 := url.Values{}
	p3.Set("q", "a!'()*b")
	rid3 := signParams(p3, "ea1db124af3c7062474693fa704f4ff8", now).Get("w_rid")
	if len(rid3) != 32 {
		t.Error("filtered-value signing failed")
	}
}

func TestParseSearch(t *testing.T) {
	body := []byte(`{"code":0,"message":"ok","data":{"result":[
		{"title":"【原神】<em class=\"keyword\">圣遗物</em>速刷路线&amp;教程","author":"UP主","bvid":"BV1xx411c7mD","play":12345,"duration":"12:34","pubdate":1700000000},
		{"title":"无bvid的占位","author":"x","bvid":"","play":1,"duration":"0:30","pubdate":1},
		{"title":"长视频","author":"y","bvid":"BV1yy411c7mE","play":99,"duration":"1:02:03","pubdate":2}
	]}}`)
	vids, err := parseSearch(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(vids) != 2 {
		t.Fatalf("want 2 videos (empty bvid skipped), got %d", len(vids))
	}
	v := vids[0]
	if v.Title != "【原神】圣遗物速刷路线&教程" {
		t.Errorf("em tags / entities not cleaned: %q", v.Title)
	}
	if v.URL != "https://www.bilibili.com/video/BV1xx411c7mD" {
		t.Errorf("url=%q", v.URL)
	}
	if v.DurationSec != 12*60+34 {
		t.Errorf("duration=%d", v.DurationSec)
	}
	if vids[1].DurationSec != 3723 {
		t.Errorf("h:m:s duration=%d", vids[1].DurationSec)
	}
}

func TestParseSearchRiskControl(t *testing.T) {
	_, err := parseSearch([]byte(`{"code":-412,"message":"request was banned"}`), 10)
	if err == nil {
		t.Fatal("expected error for -412")
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]int{"0:30": 30, "12:34": 754, "1:02:03": 3723, "bad": 0, "": 0}
	for in, want := range cases {
		if got := parseDuration(in); got != want {
			t.Errorf("parseDuration(%q)=%d want %d", in, got, want)
		}
	}
}

func TestScanLocalRoutes(t *testing.T) {
	root := t.TempDir()
	mk := func(rel string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("{}"), 0o644)
	}
	mk("蒙德/风车菊采集路线.json")
	mk("璃月/绝云椒椒_路线.json")
	mk("misc/readme.md")            // wrong ext
	mk("node_modules/风车菊采集路线.json") // skipped dir

	got := ScanLocalRoutes([]string{root}, "风车菊", 10)
	if len(got) != 1 {
		t.Fatalf("want 1 match, got %+v", got)
	}
	if got[0].Name != "风车菊采集路线" {
		t.Errorf("name=%q", got[0].Name)
	}

	// multi-token: every token must match
	if n := len(ScanLocalRoutes([]string{root}, "绝云 路线", 10)); n != 1 {
		t.Errorf("multi-token match=%d want 1", n)
	}
	if n := len(ScanLocalRoutes([]string{root}, "不存在", 10)); n != 0 {
		t.Errorf("no-match=%d want 0", n)
	}
	if n := len(ScanLocalRoutes([]string{root}, "", 10)); n != 0 {
		t.Errorf("empty keyword should match nothing, got %d", n)
	}
}

// Package discover scans the filesystem for the external tool executables (and
// project directories) the adapters need, so an operator can auto-find install
// locations instead of typing paths. It is read-only: it matches names while
// walking directories and never opens or executes anything.
package discover

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Tool describes how to recognize one external tool on disk.
type Tool struct {
	Adapter string   // adapter key: genshin/hsr/wuwa/r1999
	Name    string   // display name
	Exe     []string // matching executable filenames (case-insensitive)
	Dirs    []string // matching directory names (for project-style tools)
}

// Tools is the known signature set, matching the four adapters.
var Tools = []Tool{
	{Adapter: "genshin", Name: "BetterGI", Exe: []string{"BetterGI.exe"}},
	{Adapter: "hsr", Name: "March7thAssistant", Exe: []string{"March7th Launcher.exe", "March7thAssistant.exe"}, Dirs: []string{"March7thAssistant"}},
	{Adapter: "hsr", Name: "Fhoe-Rail", Exe: []string{"Fhoe-Rail.exe"}, Dirs: []string{"Fhoe-Rail"}},
	{Adapter: "wuwa", Name: "ok-wuthering-waves", Exe: []string{"ok-ww.exe"}},
	{Adapter: "r1999", Name: "M9A", Exe: []string{"MaaPiCli.exe"}},
}

// Candidate is one match found on disk.
type Candidate struct {
	Adapter string `json:"adapter"`
	Tool    string `json:"tool"`
	Kind    string `json:"kind"` // "exe" | "dir"
	Path    string `json:"path"`
}

// Result is the outcome of a scan.
type Result struct {
	Candidates []Candidate `json:"candidates"`
	Roots      []string    `json:"scanned_roots"`
	ElapsedMS  int64       `json:"elapsed_ms"`
	Truncated  bool        `json:"truncated"` // hit the limit or timed out
}

// Options tune a scan.
type Options struct {
	Paths    []string      // roots to scan; empty => DefaultRoots()
	MaxDepth int           // directory depth below each root (default 4)
	Timeout  time.Duration // wall-clock cap (default 30s)
	Limit    int           // max candidates (default 200)
}

// skip lists directory names that are never worth descending into.
var skip = map[string]bool{
	"windows": true, "$recycle.bin": true, "system volume information": true,
	"programdata": true, "appdata": true, "node_modules": true, ".git": true,
	".cache": true, "temp": true, "tmp": true, "winsxs": true, "$windows.~bt": true,
}

var errStop = errors.New("discover: stop")

type ref struct{ adapter, tool string }

// DefaultRoots returns sensible scan roots: every fixed drive on Windows, or
// the home directory elsewhere.
func DefaultRoots() []string {
	if runtime.GOOS == "windows" {
		var roots []string
		for c := 'A'; c <= 'Z'; c++ {
			p := string(c) + ":\\"
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				roots = append(roots, p)
			}
		}
		return roots
	}
	if home, err := os.UserHomeDir(); err == nil {
		return []string{home}
	}
	return []string{"/"}
}

// Scan walks the configured roots looking for known tools.
func Scan(ctx context.Context, opts Options) Result {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 4
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Limit <= 0 {
		opts.Limit = 200
	}
	roots := opts.Paths
	if len(roots) == 0 {
		roots = DefaultRoots()
	}

	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	exeMap := map[string][]ref{}
	dirMap := map[string][]ref{}
	for _, t := range Tools {
		for _, e := range t.Exe {
			exeMap[strings.ToLower(e)] = append(exeMap[strings.ToLower(e)], ref{t.Adapter, t.Name})
		}
		for _, d := range t.Dirs {
			dirMap[strings.ToLower(d)] = append(dirMap[strings.ToLower(d)], ref{t.Adapter, t.Name})
		}
	}

	res := Result{Roots: roots, Candidates: []Candidate{}}
	seen := map[string]bool{}
	add := func(c Candidate) {
		key := c.Adapter + "\x00" + c.Path
		if seen[key] {
			return
		}
		seen[key] = true
		res.Candidates = append(res.Candidates, c)
	}

	start := time.Now()
	for _, root := range roots {
		rootClean := filepath.Clean(root)
		_ = filepath.WalkDir(rootClean, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir // unreadable dir: skip, keep going
				}
				return nil
			}
			if ctx.Err() != nil {
				res.Truncated = true
				return errStop
			}
			if len(res.Candidates) >= opts.Limit {
				res.Truncated = true
				return errStop
			}
			name := strings.ToLower(d.Name())
			if d.IsDir() {
				if path != rootClean && skip[name] {
					return fs.SkipDir
				}
				for _, r := range dirMap[name] {
					add(Candidate{r.adapter, r.tool, "dir", path})
				}
				if depthOf(rootClean, path) >= opts.MaxDepth {
					return fs.SkipDir
				}
				return nil
			}
			for _, r := range exeMap[name] {
				add(Candidate{r.adapter, r.tool, "exe", path})
			}
			return nil
		})
		if ctx.Err() != nil || len(res.Candidates) >= opts.Limit {
			res.Truncated = true
			break
		}
	}
	res.ElapsedMS = time.Since(start).Milliseconds()
	return res
}

func depthOf(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(os.PathSeparator)) + 1
}

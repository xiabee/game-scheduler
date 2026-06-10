package guide

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// LocalRoute is a runnable route/script file found in a locally installed
// script library (e.g. a bettergi-scripts-list clone or a Fhoe-Rail map dir).
type LocalRoute struct {
	Name string `json:"name"` // filename without extension
	Path string `json:"path"` // absolute path
	Root string `json:"root"` // which configured root it came from
}

// routeExts are the file types route/script libraries use.
var routeExts = map[string]bool{".json": true, ".js": true, ".txt": true}

// localSkip mirrors the noise dirs the discover scanner ignores.
var localSkip = map[string]bool{
	".git": true, "node_modules": true, "__pycache__": true, ".cache": true,
}

// ScanLocalRoutes searches the given roots (depth-limited) for route/script
// files whose names contain every space-separated token of keyword
// (case-insensitive). Read-only; returns at most limit matches.
func ScanLocalRoutes(roots []string, keyword string, limit int) []LocalRoute {
	if limit <= 0 {
		limit = 50
	}
	tokens := strings.Fields(strings.ToLower(keyword))
	out := []LocalRoute{}
	if len(tokens) == 0 {
		return out
	}
	const maxDepth = 6
	for _, root := range roots {
		root = filepath.Clean(root)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if len(out) >= limit {
				return filepath.SkipAll
			}
			name := strings.ToLower(d.Name())
			if d.IsDir() {
				if path != root && localSkip[name] {
					return fs.SkipDir
				}
				if depth(root, path) >= maxDepth {
					return fs.SkipDir
				}
				return nil
			}
			if !routeExts[filepath.Ext(name)] {
				return nil
			}
			base := strings.TrimSuffix(name, filepath.Ext(name))
			for _, t := range tokens {
				if !strings.Contains(base, t) {
					return nil
				}
			}
			out = append(out, LocalRoute{
				Name: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())),
				Path: path,
				Root: root,
			})
			return nil
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

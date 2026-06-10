package guide

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// LocalRoute is a runnable route/script file found in a locally installed
// script library (e.g. a bettergi-scripts-list clone or a Fhoe-Rail map dir).
type LocalRoute struct {
	Name      string   `json:"name"`       // filename without extension
	Path      string   `json:"path"`       // absolute path
	Root      string   `json:"root"`       // which configured root it came from
	RouteType string   `json:"route_type"` // collect | farm | daily | event | abyss | other
	Tags      []string `json:"tags"`
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
			lr := BuildLocalRoute(root, path)
			if !matchLocalRoute(lr, tokens) {
				return nil
			}
			out = append(out, LocalRoute{
				Name:      lr.Name,
				Path:      path,
				Root:      root,
				RouteType: lr.RouteType,
				Tags:      lr.Tags,
			})
			return nil
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// ScanRouteAssets scans roots for all supported route/script files.
func ScanRouteAssets(roots []string, limit int) []LocalRoute {
	if limit <= 0 {
		limit = 500
	}
	out := []LocalRoute{}
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
			out = append(out, BuildLocalRoute(root, path))
			return nil
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// BuildLocalRoute enriches one route file with inferred type and tags.
func BuildLocalRoute(root, path string) LocalRoute {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	tags := inferTags(name, rel)
	return LocalRoute{
		Name:      name,
		Path:      path,
		Root:      root,
		RouteType: GuessRouteType(name + " " + rel),
		Tags:      tags,
	}
}

// GuessRouteType classifies route names conservatively for filtering.
func GuessRouteType(text string) string {
	s := strings.ToLower(text)
	cases := []struct {
		typ  string
		keys []string
	}{
		{"abyss", []string{"深渊", "abyss", "忘却", "虚构叙事", "混沌回忆"}},
		{"event", []string{"活动", "event"}},
		{"daily", []string{"每日", "日常", "委托", "daily", "one dragon", "onedragon"}},
		{"farm", []string{"锄地", "刷", "farm", "routefarm", "声骸", "echo"}},
		{"collect", []string{"采集", "收集", "材料", "collect", "collection", "pickup"}},
	}
	for _, c := range cases {
		for _, key := range c.keys {
			if strings.Contains(s, strings.ToLower(key)) {
				return c.typ
			}
		}
	}
	return "other"
}

func inferTags(name, rel string) []string {
	parts := []string{}
	for _, p := range strings.FieldsFunc(rel, func(r rune) bool {
		return r == filepath.Separator || r == '/' || r == '\\' || r == '_' || r == '-' || r == ' ' || r == '.'
	}) {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" && p != "json" && p != "js" && p != "txt" {
			parts = append(parts, p)
		}
	}
	rt := GuessRouteType(name + " " + rel)
	if rt != "other" {
		parts = append(parts, rt)
	}
	return dedupe(parts)
}

func matchLocalRoute(lr LocalRoute, tokens []string) bool {
	hay := strings.ToLower(lr.Name + " " + lr.Path + " " + lr.RouteType + " " + strings.Join(lr.Tags, " "))
	for _, t := range tokens {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		v = strings.TrimSpace(strings.ToLower(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
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

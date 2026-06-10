// Command ctl is a thin HTTP client for the scheduler REST API.
//
// Usage:
//
//	ctl [-server http://127.0.0.1:8080] <resource> <action> [flags]
//
// Resources/actions:
//
//	games   list | get <id> | add | update <id> | delete <id>
//	tasks   list [-game id] | get <id> | add | update <id> | delete <id> | run <id> | preflight <id>
//	routes  list [-game id] | add | delete <id>
//	plans   list | get <id> | add | update <id> | delete <id>
//	execs   list [-task id] [-status s] [-limit n] | get <id> | cancel <id>
//	discover [-paths "F:/Games;D:/Tools"]   scan disk for tool executables
//	guides   -q "<关键词>" [-game id]        search Bilibili guides + local routes
//	health
//
// "add"/"update" read a JSON body from -data '<json>' or from stdin ('-data -').
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	server := flag.String("server", envOr("GS_SERVER", "http://127.0.0.1:8080"), "scheduler server base URL")
	token := flag.String("token", envOr("GS_TOKEN", ""), "API auth token (if the server requires one)")
	data := flag.String("data", "", "JSON body for add/update; '-' reads stdin")
	gameID := flag.String("game", "", "filter by game id (tasks/routes list)")
	taskID := flag.String("task", "", "filter by task id (execs list)")
	status := flag.String("status", "", "filter by status (execs list)")
	limit := flag.String("limit", "", "limit (execs list)")
	paths := flag.String("paths", "", "scan paths for 'discover', separated by ; or ,")
	query := flag.String("q", "", "search keyword for 'guides'")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(2)
	}

	c := &client{base: strings.TrimRight(*server, "/"), token: *token, hc: &http.Client{Timeout: 30 * time.Second}}

	resource := args[0]
	action := ""
	if len(args) >= 2 {
		action = args[1]
	}
	id := ""
	if len(args) >= 3 {
		id = args[2]
	}

	q := url.Values{}
	if *gameID != "" {
		q.Set("game_id", *gameID)
	}
	if *taskID != "" {
		q.Set("task_id", *taskID)
	}
	if *status != "" {
		q.Set("status", *status)
	}
	if *limit != "" {
		q.Set("limit", *limit)
	}

	var err error
	switch resource {
	case "health":
		err = c.do("GET", "/healthz", nil)
	case "guides":
		if *query == "" {
			err = fmt.Errorf("guides requires -q '<关键词>' (and optionally -game <id>)")
		} else {
			gq := url.Values{}
			gq.Set("q", *query)
			if *gameID != "" {
				gq.Set("game_id", *gameID)
			}
			err = c.do("GET", "/api/guides/search?"+gq.Encode(), nil)
		}
	case "discover":
		body := []byte("{}")
		if *paths != "" {
			parts := strings.FieldsFunc(*paths, func(r rune) bool { return r == ';' || r == ',' })
			b, _ := json.Marshal(map[string][]string{"paths": parts})
			body = b
		}
		err = c.do("POST", "/api/discover", body)
	case "games":
		err = c.crud("/api/games", action, id, *data, nil)
	case "tasks":
		if action == "run" {
			err = c.do("POST", "/api/tasks/"+id+"/run", nil)
		} else if action == "preflight" {
			err = c.do("GET", "/api/tasks/"+id+"/preflight", nil)
		} else {
			err = c.crud("/api/tasks", action, id, *data, q)
		}
	case "routes":
		err = c.crud("/api/routes", action, id, *data, q)
	case "plans":
		err = c.crud("/api/plans", action, id, *data, nil)
	case "execs", "executions":
		switch action {
		case "cancel":
			err = c.do("POST", "/api/executions/"+id+"/cancel", nil)
		default:
			err = c.crud("/api/executions", action, id, *data, q)
		}
	default:
		err = fmt.Errorf("unknown resource %q", resource)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type client struct {
	base  string
	token string
	hc    *http.Client
}

// crud maps a generic action onto REST verbs for a collection path.
func (c *client) crud(path, action, id, data string, q url.Values) error {
	switch action {
	case "", "list":
		p := path
		if len(q) > 0 {
			p += "?" + q.Encode()
		}
		return c.do("GET", p, nil)
	case "get":
		return c.do("GET", path+"/"+id, nil)
	case "add":
		body, err := readData(data)
		if err != nil {
			return err
		}
		return c.do("POST", path, body)
	case "update":
		body, err := readData(data)
		if err != nil {
			return err
		}
		return c.do("PUT", path+"/"+id, body)
	case "delete":
		return c.do("DELETE", path+"/"+id, nil)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

func (c *client) do(method, path string, body []byte) error {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := prettify(raw)
	if resp.StatusCode >= 400 {
		fmt.Fprintln(os.Stderr, out)
		return fmt.Errorf("server returned %s", resp.Status)
	}
	if strings.TrimSpace(out) != "" {
		fmt.Println(out)
	} else {
		fmt.Println(resp.Status)
	}
	return nil
}

func readData(data string) ([]byte, error) {
	if data == "-" {
		return io.ReadAll(os.Stdin)
	}
	if data == "" {
		return nil, fmt.Errorf("this action requires -data '<json>' (or -data - for stdin)")
	}
	return []byte(data), nil
}

func prettify(raw []byte) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(b)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

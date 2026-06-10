// Package guide bridges strategy content and runnable automation: it searches
// Bilibili for guide videos (public, read-only, no credentials) and matches
// keywords against locally installed route/script libraries so a guide can be
// turned into a runnable task with one click.
//
// It deliberately does NOT attempt to "watch" videos or synthesize gameplay
// from them — videos are references for the human; the runnable artifacts are
// the community script/route files the external tools already execute.
package guide

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Video is one Bilibili search result.
type Video struct {
	Title       string `json:"title"`
	Author      string `json:"author"`
	BVID        string `json:"bvid"`
	URL         string `json:"url"`
	Play        int    `json:"play"`
	DurationSec int    `json:"duration_sec"`
	Pubdate     int64  `json:"pubdate"`
}

// Searcher is the interface the API layer consumes (stubbed in tests).
type Searcher interface {
	Search(ctx context.Context, keyword string, limit int) ([]Video, error)
}

// Client searches Bilibili's public web API using WBI request signing
// (https://github.com/SocialSisterYi/bilibili-API-collect). Read-only, no
// account, no cookies beyond the anonymous device id Bilibili itself sets.
type Client struct {
	hc *http.Client
	ua string

	mu       sync.Mutex
	mixinKey string
	keyTime  time.Time
	primed   bool
}

// NewClient builds a Bilibili search client.
func NewClient() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		hc: &http.Client{Timeout: 15 * time.Second, Jar: jar},
		ua: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36",
	}
}

// mixinKeyEncTab is Bilibili's published WBI key permutation table.
var mixinKeyEncTab = []int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35, 27, 43, 5, 49,
	33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13, 37, 48, 7, 16, 24, 55, 40,
	61, 26, 17, 0, 1, 60, 51, 30, 4, 22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11,
	36, 20, 34, 44, 52,
}

// mixinKey derives the 32-char signing key from the two WBI image keys.
func mixinKey(imgKey, subKey string) string {
	raw := imgKey + subKey
	var b strings.Builder
	for _, i := range mixinKeyEncTab {
		if i < len(raw) {
			b.WriteByte(raw[i])
		}
	}
	s := b.String()
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}

// signParams adds wts and w_rid to params per the WBI scheme.
func signParams(params url.Values, mixin string, now time.Time) url.Values {
	params.Set("wts", strconv.FormatInt(now.Unix(), 10))
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var q strings.Builder
	for i, k := range keys {
		v := strings.Map(func(r rune) rune {
			switch r {
			case '!', '\'', '(', ')', '*':
				return -1
			}
			return r
		}, params.Get(k))
		if i > 0 {
			q.WriteByte('&')
		}
		q.WriteString(url.QueryEscape(k))
		q.WriteByte('=')
		q.WriteString(url.QueryEscape(v))
	}
	sum := md5.Sum([]byte(q.String() + mixin))
	params.Set("w_rid", hex.EncodeToString(sum[:]))
	return params
}

// prime fetches the Bilibili homepage once so the anonymous device cookies
// (buvid3 etc.) exist; without them the search API rejects requests.
func (c *Client) prime(ctx context.Context) error {
	c.mu.Lock()
	done := c.primed
	c.mu.Unlock()
	if done {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://www.bilibili.com/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.ua)
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	resp.Body.Close()
	c.mu.Lock()
	c.primed = true
	c.mu.Unlock()
	return nil
}

// wbiKey returns a cached mixin key, refreshing it every 12h (Bilibili rotates
// the underlying keys daily).
func (c *Client) wbiKey(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.mixinKey != "" && time.Since(c.keyTime) < 12*time.Hour {
		k := c.mixinKey
		c.mu.Unlock()
		return k, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.bilibili.com/x/web-interface/nav", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var nav struct {
		Data struct {
			WbiImg struct {
				ImgURL string `json:"img_url"`
				SubURL string `json:"sub_url"`
			} `json:"wbi_img"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&nav); err != nil {
		return "", fmt.Errorf("guide: decode nav: %w", err)
	}
	img, sub := keyFromURL(nav.Data.WbiImg.ImgURL), keyFromURL(nav.Data.WbiImg.SubURL)
	if img == "" || sub == "" {
		return "", fmt.Errorf("guide: wbi keys unavailable")
	}
	k := mixinKey(img, sub)
	c.mu.Lock()
	c.mixinKey, c.keyTime = k, time.Now()
	c.mu.Unlock()
	return k, nil
}

// keyFromURL extracts "abc" from ".../abc.png".
func keyFromURL(u string) string {
	i := strings.LastIndex(u, "/")
	j := strings.LastIndex(u, ".")
	if i < 0 || j <= i+1 {
		return ""
	}
	return u[i+1 : j]
}

var emTag = regexp.MustCompile(`</?em[^>]*>`)

// searchResponse mirrors the fields we need from the search API.
type searchResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Result []struct {
			Title    string `json:"title"`
			Author   string `json:"author"`
			BVID     string `json:"bvid"`
			Play     int    `json:"play"`
			Duration string `json:"duration"`
			Pubdate  int64  `json:"pubdate"`
		} `json:"result"`
	} `json:"data"`
}

// parseSearch converts an API payload into Videos (exported for tests via the
// package-internal test file).
func parseSearch(body []byte, limit int) ([]Video, error) {
	var sr searchResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, fmt.Errorf("guide: decode search: %w", err)
	}
	if sr.Code != 0 {
		if sr.Code == -412 {
			return nil, fmt.Errorf("guide: bilibili 风控拦截(-412),请稍后重试")
		}
		return nil, fmt.Errorf("guide: bilibili code %d: %s", sr.Code, sr.Message)
	}
	out := make([]Video, 0, limit)
	for _, r := range sr.Data.Result {
		if r.BVID == "" {
			continue
		}
		out = append(out, Video{
			Title:       html.UnescapeString(emTag.ReplaceAllString(r.Title, "")),
			Author:      r.Author,
			BVID:        r.BVID,
			URL:         "https://www.bilibili.com/video/" + r.BVID,
			Play:        r.Play,
			DurationSec: parseDuration(r.Duration),
			Pubdate:     r.Pubdate,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// parseDuration converts "12:34" / "1:02:03" to seconds.
func parseDuration(s string) int {
	parts := strings.Split(s, ":")
	total := 0
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return total
}

// Search queries Bilibili video search for keyword.
func (c *Client) Search(ctx context.Context, keyword string, limit int) ([]Video, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, fmt.Errorf("guide: empty keyword")
	}
	if limit <= 0 || limit > 30 {
		limit = 15
	}
	if err := c.prime(ctx); err != nil {
		return nil, fmt.Errorf("guide: reach bilibili: %w", err)
	}
	mixin, err := c.wbiKey(ctx)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("search_type", "video")
	params.Set("keyword", keyword)
	params.Set("page", "1")
	params.Set("page_size", strconv.Itoa(limit))
	params = signParams(params, mixin, time.Now())

	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://api.bilibili.com/x/web-interface/wbi/search/type?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("guide: search request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	return parseSearch(body, limit)
}

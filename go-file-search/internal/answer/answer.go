// Package answer looks up a short summary for a question.
//
// With a Serper API key it uses Google results through google.serper.dev, which
// returns the answer box, knowledge panel or top result from Google. Without a
// key it falls back to Wikipedia, which needs no key.
package answer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// Result is the summary found for a question.
type Result struct {
	Title string
	Text  string
	URL   string // the page, opened when the user picks the answer
}

// ErrNoAnswer means nothing matched the question.
var ErrNoAnswer = errors.New("no answer found")

const (
	serperAPI  = "https://google.serper.dev/search"
	searchAPI  = "https://en.wikipedia.org/w/api.php"
	summaryAPI = "https://en.wikipedia.org/api/rest_v1/page/summary/"
	userAgent  = "Scope/1.0 (Windows desktop search app)"
	maxChars   = 360 // longer summaries are cut at a sentence or word boundary
)

var client = &http.Client{Timeout: 12 * time.Second}

// Fetch finds a summary for query. If serperKey is set, Google results are
// used; otherwise Wikipedia. It blocks, so call it from a goroutine.
func Fetch(query, serperKey string) (Result, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return Result{}, ErrNoAnswer
	}
	if key := strings.TrimSpace(serperKey); key != "" {
		return fetchGoogle(q, key)
	}
	return fetchWikipedia(q)
}

// Describe turns a lookup error into one short line for the search box.
func Describe(err error) string {
	var urlErr *url.Error
	switch {
	case err == nil:
		return ""
	case isUserError(err):
		return err.Error()
	case errors.Is(err, ErrNoAnswer):
		return "No answer found for that question."
	case errors.As(err, &urlErr):
		return "Couldn't reach the internet. Check your connection, proxy or firewall."
	default:
		msg := err.Error()
		if utf8.RuneCountInString(msg) > 110 {
			msg = string([]rune(msg)[:110]) + "…"
		}
		return msg
	}
}

func isUserError(err error) bool {
	var ue userError
	return errors.As(err, &ue)
}

// ---- Google results through Serper ------------------------------------------

type serperResponse struct {
	AnswerBox struct {
		Answer  string `json:"answer"`
		Snippet string `json:"snippet"`
		Title   string `json:"title"`
		Link    string `json:"link"`
	} `json:"answerBox"`
	KnowledgeGraph struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Website     string `json:"website"`
	} `json:"knowledgeGraph"`
	Organic []struct {
		Title   string `json:"title"`
		Link    string `json:"link"`
		Snippet string `json:"snippet"`
	} `json:"organic"`
}

func fetchGoogle(q, key string) (Result, error) {
	body := map[string]any{"q": q, "gl": "us", "hl": "en", "num": 5}
	var resp serperResponse
	if err := postJSONRetry(serperAPI, key, body, &resp); err != nil {
		return Result{}, err
	}

	var res Result
	ab := resp.AnswerBox
	kg := resp.KnowledgeGraph
	switch {
	case strings.TrimSpace(ab.Answer) != "":
		res = Result{Title: ab.Title, Text: ab.Answer, URL: ab.Link}
	case strings.TrimSpace(ab.Snippet) != "":
		res = Result{Title: ab.Title, Text: ab.Snippet, URL: ab.Link}
	case strings.TrimSpace(kg.Description) != "":
		res = Result{Title: kg.Title, Text: kg.Description, URL: kg.Website}
	case len(resp.Organic) > 0 && strings.TrimSpace(resp.Organic[0].Snippet) != "":
		top := resp.Organic[0]
		res = Result{Title: top.Title, Text: top.Snippet, URL: top.Link}
	default:
		return Result{}, ErrNoAnswer
	}
	res.Text = shorten(res.Text)
	if res.Title == "" {
		res.Title = q
	}
	return res, nil
}

func postJSON(u, key string, body any, v any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("X-API-KEY", key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("the Serper API key was rejected (HTTP %d); check searchApiKey in settings.json", resp.StatusCode)
	case resp.StatusCode == http.StatusPaymentRequired || resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("Serper has no credits left or is rate-limiting (HTTP %d)", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("Serper returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// postJSONRetry retries once after a network error, not after a rejected key.
func postJSONRetry(u, key string, body any, v any) error {
	err := postJSON(u, key, body, v)
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		time.Sleep(time.Second)
		err = postJSON(u, key, body, v)
	}
	return err
}

// ---- Wikipedia fallback ------------------------------------------------------

func fetchWikipedia(q string) (Result, error) {
	params := url.Values{}
	params.Set("action", "query")
	params.Set("list", "search")
	params.Set("srsearch", q)
	params.Set("srlimit", "1")
	params.Set("format", "json")
	var found struct {
		Query struct {
			Search []struct {
				Title string `json:"title"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := getJSONRetry(searchAPI+"?"+params.Encode(), &found); err != nil {
		return Result{}, err
	}
	if len(found.Query.Search) == 0 {
		return Result{}, ErrNoAnswer
	}
	title := found.Query.Search[0].Title

	var sum struct {
		Title       string `json:"title"`
		Extract     string `json:"extract"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}
	path := strings.ReplaceAll(title, " ", "_")
	if err := getJSONRetry(summaryAPI+url.PathEscape(path), &sum); err != nil {
		return Result{}, err
	}
	text := shorten(sum.Extract)
	if text == "" {
		return Result{}, ErrNoAnswer
	}
	return Result{Title: sum.Title, Text: text, URL: sum.ContentURLs.Desktop.Page}, nil
}

// getJSONRetry tries once more after a network error, since the first
// connection of a session is sometimes slow to start.
func getJSONRetry(u string, v any) error {
	err := getJSON(u, v)
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		time.Sleep(time.Second)
		err = getJSON(u, v)
	}
	return err
}

func getJSON(u string, v any) error {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrNoAnswer
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Wikipedia returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// shorten collapses whitespace and cuts the text to about maxChars, at the
// last full stop or space before the limit.
func shorten(s string) string { return shortenTo(s, maxChars) }

func shortenTo(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	cut := string(runes[:limit])
	if i := strings.LastIndex(cut, ". "); i > limit/2 {
		return cut[:i+1]
	}
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return cut + "…"
}

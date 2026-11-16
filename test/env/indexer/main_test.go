package main

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testKey = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer((&indexer{key: testKey}).handler())
	t.Cleanup(s.Close)
	return s
}

func do(t *testing.T, method, url, body string, key bool) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key {
		req.Header.Set("X-Api-Key", testKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func TestCheckKey(t *testing.T) {
	for key, ok := range map[string]bool{testKey: true, "short": false, "eeeeeeeeeeeeeeee1": false, "": false} {
		if err := checkKey(key); (err == nil) != ok {
			t.Errorf("checkKey(%q) = %v", key, err)
		}
	}
}

func TestCaps(t *testing.T) {
	s := newTestServer(t)
	code, body := do(t, "GET", s.URL+"/api?t=caps", "", false)
	var d capsDoc
	if err := xml.Unmarshal([]byte(body), &d); code != 200 || err != nil {
		t.Fatalf("caps: %d %v %s", code, err, body)
	}
	if d.Searching.MovieSearch.Available != "yes" || !strings.Contains(d.Searching.TVSearch.SupportedParams, "tvdbid") || len(d.Categories) != 2 {
		t.Errorf("caps: %+v", d)
	}
}

func TestSearchAndGet(t *testing.T) {
	s := newTestServer(t)
	if code, _ := do(t, "PUT", s.URL+"/control/releases", `[{"title":"A"}]`, false); code != 401 {
		t.Errorf("control without key: %d", code)
	}
	if code, body := do(t, "PUT", s.URL+"/control/releases", `[{"title":"A","category":2040}]`, true); code != 400 {
		t.Errorf("release without size: %d %s", code, body)
	}
	code, body := do(t, "PUT", s.URL+"/control/releases",
		`[{"title":"Some.Film.2001.1080p.BluRay.x264-FUKKU","category":2040,"size":1000,"guid":"film"},
		  {"title":"Some.Show.S01E01.1080p.BluRay.x264-FUKKU","category":5040,"size":500}]`, true)
	if code != 200 {
		t.Fatalf("put releases: %d %s", code, body)
	}

	if _, body := do(t, "GET", s.URL+"/api?t=movie&cat=2000&apikey=wrong", "", false); !strings.Contains(body, `code="100"`) {
		t.Errorf("wrong API key: %s", body)
	}
	_, body = do(t, "GET", s.URL+"/api?t=movie&cat=2000,2040&offset=0&apikey="+testKey, "", false)
	var feed rss
	if err := xml.Unmarshal([]byte(body), &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Channel.Items) != 1 || feed.Channel.Items[0].Title != "Some.Film.2001.1080p.BluRay.x264-FUKKU" || feed.Channel.Items[0].Enclosure.Length != 1000 {
		t.Fatalf("movie search: %s", body)
	}
	if _, body := do(t, "GET", s.URL+"/api?t=movie&cat=2000&offset=100&apikey="+testKey, "", false); strings.Contains(body, "<item>") {
		t.Errorf("a second page should be empty: %s", body)
	}
	_, body = do(t, "GET", s.URL+"/api?t=tvsearch&cat=5040&apikey="+testKey, "", false)
	if !strings.Contains(body, "Some.Show.S01E01") || strings.Contains(body, "Some.Film") {
		t.Errorf("tv search: %s", body)
	}

	link := feed.Channel.Items[0].Enclosure.URL
	code, body = do(t, "GET", link, "", false)
	var n nzbDoc
	if err := xml.Unmarshal([]byte(body), &n); code != 200 || err != nil || len(n.Files) != 1 || !strings.Contains(n.Files[0].Subject, "Some.Film") {
		t.Fatalf("nzb: %d %v %s", code, err, body)
	}

	_, body = do(t, "GET", s.URL+"/control/requests", "", true)
	var reqs []Request
	if err := json.Unmarshal([]byte(body), &reqs); err != nil {
		t.Fatal(err)
	}
	// The wrong-key request is not logged; the rest are.
	if len(reqs) != 4 || reqs[0].Function != "movie" || len(reqs[0].Returned) != 1 || reqs[3].Function != "get" {
		t.Errorf("requests: %s", body)
	}
	if _, ok := reqs[0].Query["apikey"]; ok {
		t.Errorf("the API key is logged: %v", reqs[0].Query)
	}
	do(t, "DELETE", s.URL+"/control/releases", "", true)
	if _, body := do(t, "GET", s.URL+"/api?t=movie&apikey="+testKey, "", false); strings.Contains(body, "<item>") {
		t.Errorf("releases not cleared: %s", body)
	}
}

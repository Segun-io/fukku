// Command indexer is the fake Newznab indexer of the test environment (make env-up). The seeder
// adds it to Radarr and Sonarr as an indexer, next to a blackhole download client; the probes
// tell it at run time which releases to offer and read back which requests it answered, so a
// probe can show that an app searched, saw a release and grabbed it (or did not).
//
// Releases are only generated names and sizes. Their NZB files name no real server and no real
// post; nothing is ever downloaded. The indexer makes no outbound connection. It is for local
// tests only.
//
//	GET    /api?t=caps                         capabilities (movie-search, tv-search, categories)
//	GET    /api?t=search|movie|tvsearch&cat=…  every offered release in those categories (RSS)
//	GET    /api?t=get&id=<guid>                the release's NZB
//	PUT    /control/releases   [Release…]       replace the offered releases (X-Api-Key)
//	GET    /control/releases                    the offered releases
//	DELETE /control/releases                    offer nothing
//	GET    /control/requests                    the search and NZB requests answered so far
//	DELETE /control/requests                    forget them
//	GET    /healthz                             200
//
// The apps filter what they get themselves (by title, year, season and episode), so the
// indexer filters only by category and never by query: it offers every release to every search.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Release is one release the indexer offers.
type Release struct {
	Title    string `json:"title"`
	Category int    `json:"category"` // a Newznab category, e.g. 2040 (Movies/HD) or 5040 (TV/HD)
	Size     int64  `json:"size"`     // bytes
	GUID     string `json:"guid,omitempty"`
	// PubDate defaults to an hour before the release was offered.
	PubDate time.Time `json:"pubDate,omitzero"`
}

// Request is one search or NZB request the indexer answered.
type Request struct {
	At       time.Time         `json:"at"`
	Agent    string            `json:"agent"` // the app's User-Agent, e.g. Radarr/6.4.4…
	Function string            `json:"t"`
	Query    map[string]string `json:"query"` // the parameters, without the API key
	Returned []string          `json:"returned"`
}

// Newznab categories in the capabilities.
var categories = []struct {
	id   int
	name string
	subs map[int]string
}{
	{2000, "Movies", map[int]string{2030: "Movies/SD", 2040: "Movies/HD", 2045: "Movies/UHD"}},
	{5000, "TV", map[int]string{5030: "TV/SD", 5040: "TV/HD", 5045: "TV/UHD"}},
}

type indexer struct {
	key      string
	mu       sync.Mutex
	releases []Release
	requests []Request
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	set := flag.NewFlagSet("indexer", flag.ContinueOnError)
	listen := set.String("listen", ":8080", "address to listen on")
	key := set.String("key", os.Getenv("FUKKU_INDEXER_KEY"), "the API key the apps and the probes send (letters only)")
	health := set.String("healthcheck", "", "check the indexer at this address and exit")
	if err := set.Parse(args); err != nil {
		return 2
	}
	if *health != "" {
		return healthcheck(*health)
	}
	if err := checkKey(*key); err != nil {
		log.Printf("indexer: %v", err)
		return 2
	}
	ix := &indexer{key: *key}
	srv := &http.Server{Addr: *listen, Handler: ix.handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("indexer: listening on %s", *listen)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := 0
	select {
	case err := <-errc:
		log.Printf("indexer: %v", err)
		code = 1
	case <-ctx.Done():
	}
	_ = srv.Close()
	return code
}

// checkKey wants a fixed fake key of letters only, like the rest of the test environment.
func checkKey(key string) error {
	if len(key) < 16 {
		return fmt.Errorf("the API key must have at least 16 letters (set -key or FUKKU_INDEXER_KEY)")
	}
	for _, c := range key {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return fmt.Errorf("the API key must be letters only")
		}
	}
	return nil
}

func healthcheck(addr string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func (ix *indexer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /api", ix.api)
	mux.HandleFunc("GET /control/releases", ix.control(func(w http.ResponseWriter, _ *http.Request) {
		ix.mu.Lock()
		defer ix.mu.Unlock()
		writeJSON(w, ix.releases)
	}))
	mux.HandleFunc("PUT /control/releases", ix.control(ix.putReleases))
	mux.HandleFunc("DELETE /control/releases", ix.control(func(w http.ResponseWriter, _ *http.Request) {
		ix.mu.Lock()
		defer ix.mu.Unlock()
		ix.releases = nil
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("GET /control/requests", ix.control(func(w http.ResponseWriter, _ *http.Request) {
		ix.mu.Lock()
		defer ix.mu.Unlock()
		writeJSON(w, ix.requests)
	}))
	mux.HandleFunc("DELETE /control/requests", ix.control(func(w http.ResponseWriter, _ *http.Request) {
		ix.mu.Lock()
		defer ix.mu.Unlock()
		ix.requests = nil
		w.WriteHeader(http.StatusNoContent)
	}))
	return mux
}

func (ix *indexer) control(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != ix.key {
			http.Error(w, "wrong or missing X-Api-Key", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (ix *indexer) putReleases(w http.ResponseWriter, r *http.Request) {
	var rs []Release
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&rs); err != nil {
		http.Error(w, "body: "+err.Error(), http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	for i := range rs {
		if strings.TrimSpace(rs[i].Title) == "" || rs[i].Size <= 0 || categoryName(rs[i].Category) == "" {
			http.Error(w, fmt.Sprintf("release %d needs a title, a size and a known category", i), http.StatusBadRequest)
			return
		}
		if rs[i].GUID == "" {
			rs[i].GUID = newGUID()
		}
		if rs[i].PubDate.IsZero() {
			rs[i].PubDate = now.Add(-time.Hour)
		}
	}
	ix.mu.Lock()
	ix.releases = rs
	ix.mu.Unlock()
	writeJSON(w, rs)
}

func newGUID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if v == nil {
		v = []any{}
	}
	_ = json.NewEncoder(w).Encode(v)
}

// ---- Newznab ----------------------------------------------------------------------------

func (ix *indexer) api(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fn := q.Get("t")
	if fn == "caps" {
		writeXML(w, caps())
		return
	}
	if q.Get("apikey") != ix.key {
		writeXML(w, newznabError{Code: 100, Description: "Incorrect user credentials"})
		return
	}
	switch fn {
	case "search", "movie", "tvsearch", "tv":
		ix.search(w, r, fn)
	case "get":
		ix.nzb(w, r, q.Get("id"))
	default:
		writeXML(w, newznabError{Code: 202, Description: "No such function"})
	}
}

func (ix *indexer) search(w http.ResponseWriter, r *http.Request, fn string) {
	q := r.URL.Query()
	var cats []int
	for c := range strings.SplitSeq(q.Get("cat"), ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(c)); err == nil {
			cats = append(cats, n)
		}
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	ix.mu.Lock()
	var items []Release
	if offset == 0 {
		for _, rel := range ix.releases {
			if len(cats) == 0 || slices.Contains(cats, rel.Category) || slices.Contains(cats, rel.Category/1000*1000) {
				items = append(items, rel)
			}
		}
	}
	ix.log(r, fn, items)
	ix.mu.Unlock()

	base := "http://" + r.Host
	feed := rss{Version: "2.0", Channel: channel{Title: "fukku fake indexer", Description: "generated releases for the test environment"}}
	for _, rel := range items {
		link := base + "/api?t=get&id=" + rel.GUID + "&apikey=" + ix.key
		feed.Channel.Items = append(feed.Channel.Items, item{
			Title:     rel.Title,
			GUID:      guid{IsPermaLink: false, Value: rel.GUID},
			Link:      link,
			Comments:  base + "/details/" + rel.GUID,
			PubDate:   rel.PubDate.UTC().Format(time.RFC1123Z),
			Category:  categoryName(rel.Category),
			Enclosure: enclosure{URL: link, Length: rel.Size, Type: "application/x-nzb"},
		})
	}
	writeXML(w, feed)
}

func (ix *indexer) nzb(w http.ResponseWriter, r *http.Request, id string) {
	ix.mu.Lock()
	var rel *Release
	for i := range ix.releases {
		if ix.releases[i].GUID == id {
			rel = &ix.releases[i]
		}
	}
	var returned []Release
	if rel != nil {
		returned = []Release{*rel}
	}
	ix.log(r, "get", returned)
	ix.mu.Unlock()
	if rel == nil {
		writeXML(w, newznabError{Code: 300, Description: "No such item"})
		return
	}
	w.Header().Set("Content-Type", "application/x-nzb")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", rel.Title+".nzb"))
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(nzbFor(*rel))
}

// log records a request; ix.mu is held.
func (ix *indexer) log(r *http.Request, fn string, returned []Release) {
	query := map[string]string{}
	for k, v := range r.URL.Query() {
		if k != "apikey" {
			query[k] = strings.Join(v, ",")
		}
	}
	titles := []string{}
	for _, rel := range returned {
		titles = append(titles, rel.Title)
	}
	ix.requests = append(ix.requests, Request{At: time.Now().UTC(), Agent: r.UserAgent(), Function: fn, Query: query, Returned: titles})
}

func categoryName(id int) string {
	for _, c := range categories {
		if c.id == id {
			return c.name
		}
		if s, ok := c.subs[id]; ok {
			return s
		}
	}
	return ""
}

func writeXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(v)
}

type newznabError struct {
	XMLName     xml.Name `xml:"error"`
	Code        int      `xml:"code,attr"`
	Description string   `xml:"description,attr"`
}

type rss struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel channel  `xml:"channel"`
}

type channel struct {
	Title       string `xml:"title"`
	Description string `xml:"description"`
	Items       []item `xml:"item"`
}

type item struct {
	Title     string    `xml:"title"`
	GUID      guid      `xml:"guid"`
	Link      string    `xml:"link"`
	Comments  string    `xml:"comments"`
	PubDate   string    `xml:"pubDate"`
	Category  string    `xml:"category"`
	Enclosure enclosure `xml:"enclosure"`
}

type guid struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type enclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type capsDoc struct {
	XMLName    xml.Name     `xml:"caps"`
	Server     capsServer   `xml:"server"`
	Limits     capsLimits   `xml:"limits"`
	Searching  capsSearch   `xml:"searching"`
	Categories []capsCatDoc `xml:"categories>category"`
}

type capsServer struct {
	Version string `xml:"version,attr"`
	Title   string `xml:"title,attr"`
}

type capsLimits struct {
	Max     int `xml:"max,attr"`
	Default int `xml:"default,attr"`
}

type capsSearch struct {
	Search      capsFunc `xml:"search"`
	TVSearch    capsFunc `xml:"tv-search"`
	MovieSearch capsFunc `xml:"movie-search"`
}

type capsFunc struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

type capsCatDoc struct {
	ID      int          `xml:"id,attr"`
	Name    string       `xml:"name,attr"`
	Subcats []capsSubcat `xml:"subcat"`
}

type capsSubcat struct {
	ID   int    `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

func caps() capsDoc {
	d := capsDoc{
		Server: capsServer{Version: "1.0", Title: "fukku fake indexer"},
		Limits: capsLimits{Max: 100, Default: 100},
		Searching: capsSearch{
			Search:      capsFunc{"yes", "q"},
			TVSearch:    capsFunc{"yes", "q,season,ep,tvdbid"},
			MovieSearch: capsFunc{"yes", "q,imdbid,tmdbid"},
		},
	}
	for _, c := range categories {
		cd := capsCatDoc{ID: c.id, Name: c.name}
		ids := make([]int, 0, len(c.subs))
		for id := range c.subs {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			cd.Subcats = append(cd.Subcats, capsSubcat{ID: id, Name: c.subs[id]})
		}
		d.Categories = append(d.Categories, cd)
	}
	return d
}

// ---- NZB --------------------------------------------------------------------------------

type nzbDoc struct {
	XMLName xml.Name  `xml:"nzb"`
	Files   []nzbFile `xml:"file"`
}

type nzbFile struct {
	Poster   string       `xml:"poster,attr"`
	Date     int64        `xml:"date,attr"`
	Subject  string       `xml:"subject,attr"`
	Groups   []string     `xml:"groups>group"`
	Segments []nzbSegment `xml:"segments>segment"`
}

type nzbSegment struct {
	Bytes  int64  `xml:"bytes,attr"`
	Number int    `xml:"number,attr"`
	ID     string `xml:",chardata"`
}

// nzbFor is a well-formed NZB that names one file and one segment, neither of which exists.
func nzbFor(rel Release) nzbDoc {
	return nzbDoc{Files: []nzbFile{{
		Poster:   "fukku fake indexer <indexer@example.com>",
		Date:     rel.PubDate.Unix(),
		Subject:  fmt.Sprintf("%q yEnc (1/1)", rel.Title+".mkv"),
		Groups:   []string{"alt.binaries.example"},
		Segments: []nzbSegment{{Bytes: rel.Size, Number: 1, ID: rel.GUID + "-1@example.com"}},
	}}}
}

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
)

type flags struct {
	set                                       *flag.FlagSet
	listen, control, upstream, signin, health *string
}

func newFlags() flags {
	set := flag.NewFlagSet("proxy", flag.ContinueOnError)
	return flags{
		set:      set,
		listen:   set.String("listen", ":8080", "address of the public listener"),
		control:  set.String("control", ":8081", "address of the control API listener"),
		upstream: set.String("upstream", "http://fukku-web:8080", "Fukku web process; the host must be a compose service name"),
		signin:   set.String("signin-url", "http://127.0.0.1:18080/__test/signin", "where expired-302 redirects to"),
		health:   set.String("healthcheck", "", "container health check: probe the control listener at this address (e.g. 127.0.0.1:8081) and exit"),
	}
}

// state is what the control API changes.
type state struct {
	mu       sync.Mutex
	mode     string
	cutAfter int64
	cutTimes int
}

func newState() state { return state{mode: modeOK} }

func (s *state) getMode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *state) setMode(m string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
}

func (s *state) setCut(after int64, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutAfter, s.cutTimes = after, times
}

// takeCut consumes one pending cut.
func (s *state) takeCut() (after int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cutTimes <= 0 {
		return 0, false
	}
	s.cutTimes--
	return s.cutAfter, true
}

func (s *state) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode, s.cutAfter, s.cutTimes = modeOK, 0, 0
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<16))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		http.Error(w, "bad JSON body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (p *proxy) controlSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if !decode(w, r, &body) {
		return
	}
	switch body.Mode {
	case modeOK, modeExpired401, modeExpired302:
		p.state.setMode(body.Mode)
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, fmt.Sprintf("mode %q is not one of ok, expired-401, expired-302", body.Mode), http.StatusBadRequest)
	}
}

func (p *proxy) controlCut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AfterBytes *int64 `json:"afterBytes"`
		Times      *int   `json:"times"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AfterBytes == nil || *body.AfterBytes < 0 || body.Times == nil || *body.Times < 1 {
		http.Error(w, "afterBytes (>= 0) and times (>= 1) are required", http.StatusBadRequest)
		return
	}
	p.state.setCut(*body.AfterBytes, *body.Times)
	w.WriteHeader(http.StatusNoContent)
}

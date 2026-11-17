package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"time"
)

// stubUsers are the people the stub signs in, with the group header of each. Fake, fixed.
var stubUsers = map[string]string{
	"admin1": "users,media-admins",
	"user1":  "users",
}

const stubCookie = "stub_session"

// authstub is the forward-authentication service of docs/reverse-proxy.md: GET /verify
// answers 2xx with Remote-User, Remote-Email and Remote-Groups for a signed-in request and a
// redirect to a log-in page otherwise. GET /login?user=<name> signs a user in (it sets the
// cookie), which stands for the log-in page of a real service.
func authstub(args []string) error {
	fs := flag.NewFlagSet("authstub", flag.ContinueOnError)
	listen := fs.String("listen", ":9091", "address to listen on")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		if _, ok := stubUsers[user]; !ok {
			http.Error(w, "unknown user", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: stubCookie, Value: user, Path: "/", HttpOnly: true})
		_, _ = fmt.Fprintf(w, "signed in as %s\n", user)
	})
	mux.HandleFunc("GET /verify", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(stubCookie)
		if err != nil {
			http.Redirect(w, r, "http://auth.example.com/login", http.StatusFound)
			return
		}
		groups, ok := stubUsers[c.Value]
		if !ok {
			http.Redirect(w, r, "http://auth.example.com/login", http.StatusFound)
			return
		}
		w.Header().Set("Remote-User", c.Value)
		w.Header().Set("Remote-Email", c.Value+"@example.com")
		w.Header().Set("Remote-Groups", groups)
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ping fails unless GET <url> answers 2xx: the health check of a container that has no shell
// or curl.
func ping(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: ping <url>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, args[0], nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("%s: %s", args[0], res.Status)
	}
	return nil
}

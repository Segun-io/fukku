// Command fukku is the Fukku server. One binary, several roles:
//
//	fukku web           the web process (UI, API, uploads); holds the API keys
//	fukku worker        the worker process (parses uploads); no network, no secrets
//	fukku combined      both in one process (mode=combined; weaker isolation)
//	fukku check-config  load, validate and print the configuration with secrets redacted
//	fukku healthcheck   container health check (web: /readyz, worker: heartbeat age)
//	fukku version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/retention"
	"github.com/segun-io/fukku/internal/runner"
	"github.com/segun-io/fukku/internal/spool"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/subtitles"
	"github.com/segun-io/fukku/internal/version"
	"github.com/segun-io/fukku/internal/web"
	"github.com/segun-io/fukku/internal/worker"
	"github.com/segun-io/fukku/ui"
)

const usage = `usage: fukku <command> [flags]

commands:
  web            run the web process
  worker         run the worker process
  combined       run web and worker in one process (mode=combined)
  check-config   validate the configuration and print it, secrets redacted
  healthcheck    exit 0 if this container is healthy
  version        print the version

Configuration: --config <file> or FUKKU_CONFIG, plus FUKKU_* environment variables
(docs/settings.md).
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	fset := flag.NewFlagSet("fukku "+cmd, flag.ContinueOnError)
	fset.SetOutput(stderr)
	configFile := fset.String("config", os.Getenv("FUKKU_CONFIG"), "YAML configuration file")
	insecureDev := fset.Bool("insecure-dev", false, "allow auth.mode=none (development only)")
	role := fset.String("role", "web", "check-config: role to validate for (web, worker, combined)")
	checkWorker := fset.Bool("worker", false, "healthcheck: check the worker heartbeat instead of the web check")

	switch cmd {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "fukku %s (%s)\n", version.Version, version.Commit)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "web", "worker", "combined", "check-config", "healthcheck":
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err := fset.Parse(rest); err != nil {
		return 2
	}

	cfg, err := config.Load(config.Options{File: *configFile})
	if err != nil {
		fmt.Fprintf(stderr, "configuration: %v\n", err)
		return 1
	}

	r := config.Role(cmd)
	switch cmd {
	case "check-config":
		r = config.Role(*role)
	case "healthcheck":
		if *checkWorker {
			return healthcheckWorker(cfg, stderr)
		}
		return healthcheckWeb(cfg, stderr)
	}
	if err := config.Validate(cfg, r, config.ValidateOptions{InsecureDev: *insecureDev}); err != nil {
		fmt.Fprintf(stderr, "configuration is invalid:\n%v\n", err)
		return 1
	}
	if cmd == "check-config" {
		out, err := yaml.Marshal(cfg)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "# valid for role %s; secrets redacted\n%s", r, out)
		return 0
	}

	log := newLogger(cfg.Server.LogFormat, stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "web":
		err = runWeb(ctx, cfg, log)
	case "worker":
		err = worker.New(cfg, log, worker.Handlers(cfg)).Run(ctx)
	case "combined":
		err = runCombined(ctx, cfg, log, worker.Handlers(cfg))
	}
	if err != nil {
		log.Error("exit", "error", err)
		return 1
	}
	return 0
}

// runWeb runs the web process with its dependencies.
func runWeb(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	q, err := spool.Open(cfg.Worker.SpoolDir)
	if err != nil {
		return err
	}
	defer func() { _ = q.Close() }()
	st, err := store.Open(ctx, store.Options{Dir: cfg.Server.DataDir, Log: log})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	swept := make(chan struct{})
	go func() {
		defer close(swept)
		_ = retention.New(cfg, st, q, log, nil).Run(ctx)
	}()
	// One set of app clients and of job locks for every step that writes a job from what
	// it read: the match API (#37) and the proposal (#38), whose matched step the runner
	// runs.
	deps := web.Deps{Queue: q, Store: st, Locks: flows.NewJobLocks()}
	if cfg.Radarr.URL != "" {
		deps.Radarr = arr.NewRadarr(cfg.Radarr)
	}
	if cfg.Sonarr.URL != "" {
		deps.Sonarr = arr.NewSonarr(cfg.Sonarr)
	}
	deps.Proposals = flows.NewProposals(cfg, log, st, deps.Radarr, deps.Sonarr, deps.Locks)
	// The import engine (#40) and its post-processing steps, in the order they run.
	deps.Imports = flows.NewEngine(deps.Proposals)
	deps.Imports.AddPostStep(subtitleStep(cfg, st))
	// The job runner (#29) drives jobs next to the HTTP server and stops with it, before
	// the store closes.
	rn := runner.New(cfg, log, st, q, time.Now)
	rn.Handle(store.Matched, deps.Proposals.Matched)
	rn.Handle(store.Importing, deps.Imports.Importing)
	rn.Handle(store.Imported, deps.Imports.Imported)
	rn.Handle(store.PostProcessing, deps.Imports.PostProcessing)
	rn.ResumeWhen(flows.CodeArrUnavailable, "the app answers again", deps.Proposals.AppAnswers)
	ran := make(chan error, 1)
	go func() { ran <- rn.Run(ctx) }()
	err = web.New(cfg, log, ui.FS(), deps).Run(ctx)
	cancel()
	<-swept
	err = errors.Join(err, <-ran)
	deps.Imports.Wait() // the post-processing goroutines end with ctx
	return errors.Join(err, st.Close())
}

// subtitleStep is the post-processing step that hands uploaded subtitles to the subtitle
// manager (#42); with provider none it is recorded as skipped.
func subtitleStep(cfg config.Config, st store.Store) *subtitles.Step {
	s := &subtitles.Step{Store: st, IntakeRoot: cfg.Paths.IntakeRoot, Config: cfg.Subtitles}
	if cfg.Subtitles.Provider == "bazarr" && cfg.Subtitles.URL != "" {
		s.Manager = subtitles.NewBazarr(cfg.Subtitles.URL, cfg.Subtitles.APIKey.Reveal(), cfg.Subtitles.RequestTimeout.D())
	}
	return s
}

// runCombined runs the same web process and worker as split mode, in one process.
func runCombined(ctx context.Context, cfg config.Config, log *slog.Logger, h map[string]worker.Handler) error {
	log.Warn("combined_mode", "note", "the worker shares the web process's network and secrets; use split mode where you can")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 2)
	go func() { errc <- runWeb(ctx, cfg, log) }()
	go func() { errc <- worker.New(cfg, log, h).Run(ctx) }()
	first := <-errc
	cancel()
	return errors.Join(first, <-errc)
}

func newLogger(format string, w io.Writer) *slog.Logger {
	if format == "json" {
		return slog.New(slog.NewJSONHandler(w, nil))
	}
	return slog.New(slog.NewTextHandler(w, nil))
}

func healthcheckWeb(cfg config.Config, stderr io.Writer) int {
	host, port, err := net.SplitHostPort(cfg.Server.ListenAddress)
	if err != nil {
		fmt.Fprintf(stderr, "server.listenAddress: %v\n", err)
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/readyz?local=1", nil)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "readyz returned %d\n", resp.StatusCode)
		return 1
	}
	return 0
}

func healthcheckWorker(cfg config.Config, stderr io.Writer) int {
	age, err := worker.HeartbeatAge(cfg.Worker.SpoolDir, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if age > cfg.Worker.HeartbeatMaxAge.D() {
		fmt.Fprintf(stderr, "worker heartbeat is %s old (limit %s)\n", age.Round(time.Second), cfg.Worker.HeartbeatMaxAge)
		return 1
	}
	return 0
}

//go:build probes

package probes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/segun-io/fukku/test/fixtures"
	"github.com/segun-io/fukku/test/testenv"
)

var (
	env    *testenv.Env
	online bool
	// simulateOutage makes every lookup and add behave as if the *arr metadata services were
	// unreachable, without touching the internet (FUKKU_TEST_SIMULATE_METADATA_OUTAGE=1).
	simulateOutage bool
)

func TestMain(m *testing.M) {
	var err error
	env, err = testenv.FromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	online = os.Getenv("FUKKU_TEST_ONLINE") == "1"
	simulateOutage = os.Getenv("FUKKU_TEST_SIMULATE_METADATA_OUTAGE") == "1"
	runTag = "R" + strings.ToUpper(strconv.FormatInt(time.Now().Unix()%(36*36*36*36), 36))
	// Jellyfin keeps items (and merges) by path, so every run uses fresh folder names and
	// first removes the folders of earlier runs.
	for _, root := range []string{filepath.Join(env.Data, "media", "movies"), filepath.Join(env.Data, "media", "tv")} {
		old, _ := filepath.Glob(filepath.Join(root, "Probe *"))
		for _, d := range old {
			_ = os.RemoveAll(d)
		}
	}
	os.Exit(m.Run())
}

// runTag makes this run's Jellyfin folder names unique.
var runTag string

// uniq inserts the run tag into a probe title: "Probe Merge (2004)" → "Probe Merge R1A2B (2004)".
func uniq(name string) string {
	if i := strings.LastIndex(name, " ("); i > 0 {
		return name[:i] + " " + runTag + name[i:]
	}
	return name + " " + runTag
}

// probe records one behaviour probe. Use observe for what was seen, then exactly one of
// confirm, differ, partial or observed for the verdict.
type probe struct {
	t     *testing.T
	ctx   context.Context
	r     Result
	start time.Time
	// rest are probes of the same test that cannot run if this one is skipped upstream.
	rest []spec
}

// spec is a probe's identity, kept apart so a probe that never gets to run can still be
// recorded (as skipped) when an earlier probe of its test is.
type spec struct {
	id, title, claim string
	apps             []string
}

func (s spec) result() Result {
	return Result{ID: s.id, Title: s.title, Claim: s.claim, Apps: s.apps, Online: true, Evidence: map[string]any{}}
}

// probe starts the probe described by s; it needs the metadata services.
func (s spec) probe(t *testing.T) *probe {
	t.Helper()
	return newProbe(t, s.id, s.title, s.claim, true, s.apps...)
}

func newProbe(t *testing.T, id, title, claim string, needsOnline bool, apps ...string) *probe {
	t.Helper()
	p := &probe{t: t, ctx: t.Context(), start: time.Now(), r: Result{
		ID: id, Test: t.Name(), Title: title, Claim: claim, Apps: apps, Online: needsOnline, Evidence: map[string]any{},
	}}
	t.Cleanup(func() {
		if p.r.Verdict == "" {
			p.r.Verdict = Error
			if !t.Failed() && !t.Skipped() {
				p.r.Error = "probe ended without a verdict"
			}
		}
		p.r.Duration = time.Since(p.start).Round(time.Second).String()
		p.r.At = time.Now().UTC()
		if err := p.r.Write(env.Results); err != nil {
			t.Errorf("write result: %v", err)
		}
	})
	if needsOnline && !online {
		p.r.Verdict = Skipped
		p.r.Reason = "needs the internet (make probes-online)"
		p.r.Observed = []string{"needs the *arr metadata services (run make probes-online)"}
		t.Skip("needs the internet: make probes-online")
	}
	return p
}

func (p *probe) observe(format string, args ...any) {
	p.t.Helper()
	msg := fmt.Sprintf(format, args...)
	p.r.Observed = append(p.r.Observed, msg)
	p.t.Log(msg)
}

func (p *probe) evidence(key string, v any) { p.r.Evidence[key] = v }

func (p *probe) confirm() { p.r.Verdict = Confirmed }

func (p *probe) observed() { p.r.Verdict = Observed }

func (p *probe) partial(why string) {
	p.r.Verdict = Partial
	p.observe("partial: %s", why)
}

// differ marks the probe red: the plan's claim does not hold for these versions.
func (p *probe) differ(format string, args ...any) {
	p.t.Helper()
	p.r.Verdict = Differs
	msg := fmt.Sprintf(format, args...)
	p.r.Observed = append(p.r.Observed, "DIFFERS: "+msg)
	p.t.Errorf("behaviour differs from the plan: %s", msg)
}

// must stops the probe with an error verdict when err is not nil.
func (p *probe) must(err error, what string) {
	p.t.Helper()
	if err != nil {
		p.r.Verdict = Error
		p.r.Error = what + ": " + err.Error()
		p.t.Fatalf("%s: %v", what, err)
	}
}

// alsoSkips names probes of the same test that depend on this one's setup: if it ends as
// skipped because the metadata service is unavailable, they are recorded as skipped too.
func (p *probe) alsoSkips(rest ...spec) { p.rest = rest }

// outage skips the probe (and those named by alsoSkips) because the *arr metadata service
// is unavailable.
func (p *probe) outage(detail string, inferred ...string) {
	p.t.Helper()
	p.r.Verdict = Skipped
	p.r.Reason = UpstreamUnavailable
	if len(inferred) > 0 {
		p.r.Reason += " (inferred: " + inferred[0] + ")"
	}
	p.observe("skipped: %s (%s)", UpstreamUnavailable, detail)
	for _, s := range p.rest {
		r := s.result()
		r.Verdict, r.Reason = Skipped, p.r.Reason
		r.Observed = []string{"skipped with " + p.r.ID + ": " + UpstreamUnavailable}
		r.At = time.Now().UTC()
		if err := r.Write(env.Results); err != nil {
			p.t.Errorf("write result: %v", err)
		}
	}
	p.t.Skip(UpstreamUnavailable)
}

// meta is must for a lookup or an add through the *arr app to its metadata service: when
// that service is unreachable (a timeout, or a 5xx saying so) the probe is skipped, not
// failed. Every other error stays an error.
func (p *probe) meta(err error, what string) {
	p.t.Helper()
	if MetadataUnavailable(err) {
		p.outage(what + ": " + err.Error())
	}
	p.must(err, what)
}

// needMeta is the first line of every lookup and add: it skips when the outage is simulated,
// before the call is made.
func (p *probe) needMeta(what string) {
	p.t.Helper()
	if simulateOutage {
		p.outage(what + ": simulated by FUKKU_TEST_SIMULATE_METADATA_OUTAGE=1")
	}
}

func (p *probe) check(ok bool, what string) {
	p.t.Helper()
	if !ok {
		p.must(fmt.Errorf("precondition failed"), what)
	}
}

// ---- files ----------------------------------------------------------------------------

// video writes a generated video to dst. Each distinct spec is generated once per
// environment and copied, so long, realistic durations stay cheap.
func (p *probe) video(dst string, spec fixtures.Video) {
	p.t.Helper()
	ext := filepath.Ext(dst)
	sum := sha256.Sum256(fmt.Appendf(nil, "v1 %s %+v", ext, spec))
	cache := filepath.Join(env.Data, ".fixture-cache", hex.EncodeToString(sum[:8])+ext)
	if _, err := os.Stat(cache); err != nil {
		p.must(os.MkdirAll(filepath.Dir(cache), 0o750), "fixture cache")
		tmp := cache + ".partial" + ext
		p.must(fixtures.Generate(p.ctx, tmp, spec), "generate "+filepath.Base(dst))
		p.must(os.Rename(tmp, cache), "fixture cache")
	}
	p.must(os.MkdirAll(filepath.Dir(dst), 0o750), "mkdir "+filepath.Dir(dst))
	p.must(copyFile(cache, dst), "copy fixture to "+dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// fresh removes and recreates a directory the probe owns.
func (p *probe) fresh(dir string) string {
	p.t.Helper()
	if !strings.HasPrefix(dir, env.Data+"/") {
		p.must(fmt.Errorf("%s is outside the test data", dir), "fresh")
	}
	p.must(os.RemoveAll(dir), "clean "+dir)
	p.must(os.MkdirAll(dir, 0o750), "mkdir "+dir)
	return dir
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func inode(path string) uint64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return s.Ino
	}
	return 0
}

func listDir(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			out = append(out, rel)
		}
		return nil
	})
	return out
}

func movies(name string) string { return filepath.Join(env.Data, "media", "movies", name) }
func shows(name string) string  { return filepath.Join(env.Data, "media", "tv", name) }
func intake(name string) string { return filepath.Join(env.Data, "intake", name) }

// Short Jellyfin fixtures: Jellyfin has no sample detection, so 10 s is enough.
var short = fixtures.Video{Duration: 10 * time.Second, Width: 1920, Height: 1080, FrameRate: "5",
	Audio: []fixtures.Audio{{Language: "eng", Default: true}}}

// long returns a realistic-length 1080p file (the *arr apps reject short files as samples).
func long(minutes int, langs ...string) fixtures.Video { return longAt(minutes, 1920, 1080, langs...) }

// longAt is long at another resolution (the *arr apps use it when a name has no quality).
func longAt(minutes, width, height int, langs ...string) fixtures.Video {
	v := fixtures.Video{Duration: time.Duration(minutes) * time.Minute, Width: width, Height: height, FrameRate: "5"}
	for i, l := range langs {
		v.Audio = append(v.Audio, fixtures.Audio{Language: l, Default: i == 0, ToneHz: 440 + 220*i})
	}
	return v
}

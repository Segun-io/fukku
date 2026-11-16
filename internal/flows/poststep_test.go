package flows

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
)

// postJob is a film job imported and moved to post-processing.
func (x *ienv) postJob() store.Job {
	x.t.Helper()
	j := x.confirm(x.filmJob())
	for range 10 {
		if j = x.jobNow(j); j.State == store.PostProcessing {
			return j
		}
		if _, err := x.step(x.eng, j); err != nil {
			x.t.Fatal(err)
		}
		x.appWorks()
	}
	x.t.Fatalf("job %s", j.State)
	return j
}

func (x *ienv) steps(j store.Job) map[string]store.PostStep {
	x.t.Helper()
	ps, err := x.st.PostSteps(x.ctx, j.ID)
	if err != nil {
		x.t.Fatal(err)
	}
	out := map[string]store.PostStep{}
	for _, p := range ps {
		out[p.Name] = p
	}
	return out
}

// Each step's outcome is recorded: done is ok, Skip is skipped with its reason, GiveUp is
// failed at once (its own retries are one attempt); a plain error is retried after
// server.postStepBackoff, doubling, until server.postStepAttempts runs, then failed. The
// job ends done all the same, and the steps it waits for do not hold up the others.
func TestPostSteps(t *testing.T) {
	x := newImportEnv(t)
	x.cfg.Server.PostStepAttempts = 3
	x.cfg.Server.PostStepBackoff = 30 * 1e9
	x.rebuild(x.movies, x.series)
	ok := &recordingStep{name: "ok"}
	skip := &recordingStep{name: "skip", answer: func(int) (bool, string, error) { return true, "", Skip("provider none") }}
	gaveUp := &recordingStep{name: "gaveUp", answer: func(int) (bool, string, error) {
		return false, "", GiveUp(errors.New("not written: Film.es.srt"))
	}}
	flaky := &recordingStep{name: "flaky", answer: func(n int) (bool, string, error) {
		if n < 2 {
			return false, "", errors.New("connection refused")
		}
		return true, "item 42", nil
	}}
	broken := &recordingStep{name: "broken", answer: func(int) (bool, string, error) { return false, "", errors.New("http://10.0.0.5: 500") }}
	notYet := &recordingStep{name: "notYet", answer: func(int) (bool, string, error) { return false, "still scanning", nil }}
	for _, s := range []PostStep{ok, skip, gaveUp, flaky, broken, notYet} {
		x.eng.AddPostStep(s)
	}
	j := x.postJob()
	if iv, rv := x.detail(j); iv.Status != ImportDone || rv == nil || len(rv.PostProcessing) != 6 || rv.PostProcessing[0].Status != store.StepPending {
		t.Fatalf("detail before: %+v %+v", iv, rv)
	}

	if j, _ = x.step(x.eng, j); j.State != store.PostProcessing {
		t.Fatalf("job %s", j.State)
	}
	s := x.steps(j)
	for name, want := range map[string]string{"ok": store.StepOK, "skip": store.StepSkipped, "gaveUp": store.StepFailed,
		"flaky": store.StepPending, "broken": store.StepPending, "notYet": store.StepPending} {
		if s[name].Status != want || s[name].Attempts != 1 {
			t.Errorf("%s: %+v, want %s after one attempt", name, s[name], want)
		}
	}
	if s["skip"].Detail != "provider none" || s["ok"].Detail != "ok" || !strings.Contains(s["gaveUp"].Detail, "Film.es.srt") {
		t.Errorf("details %+v", s)
	}

	// Within the backoff nothing runs again.
	x.clock.advance(29 * time.Second)
	if j, _ = x.step(x.eng, j); flaky.count() != 1 || j.State != store.PostProcessing {
		t.Fatalf("ran within the backoff: %d", flaky.count())
	}
	x.clock.advance(2 * time.Second)
	j, _ = x.step(x.eng, j)
	s = x.steps(j)
	if s["flaky"].Status != store.StepOK || s["flaky"].Attempts != 2 || s["broken"].Attempts != 2 || s["broken"].Status != store.StepPending {
		t.Fatalf("second round %+v", s)
	}
	// The third run is after twice the backoff.
	x.clock.advance(59 * time.Second)
	if j, _ = x.step(x.eng, j); broken.count() != 2 {
		t.Fatalf("ran within the doubled backoff: %d", broken.count())
	}
	x.clock.advance(2 * time.Second)
	if j, _ = x.step(x.eng, j); j.State != store.Done {
		t.Fatalf("job %s", j.State)
	}
	s = x.steps(j)
	if s["broken"].Status != store.StepFailed || s["broken"].Attempts != 3 || s["notYet"].Status != store.StepFailed {
		t.Errorf("final %+v", s)
	}
	if ok.count() != 1 || skip.count() != 1 || gaveUp.count() != 1 || broken.count() != 3 {
		t.Errorf("runs ok %d skip %d gaveUp %d broken %d", ok.count(), skip.count(), gaveUp.count(), broken.count())
	}

	// The result: every step in order; a failed step's detail only for an admin.
	_, rv := x.detail(j)
	names := []string{}
	for _, v := range rv.PostProcessing {
		names = append(names, v.Step+":"+v.Status)
	}
	if got := strings.Join(names, " "); got != "ok:ok skip:skipped gaveUp:failed flaky:ok broken:failed notYet:failed" {
		t.Errorf("steps %s", got)
	}
	_, user, err := x.eng.Detail(x.ctx, j, false)
	if err != nil {
		t.Fatal(err)
	}
	if user.PostProcessing[4].Detail != "" || user.PostProcessing[3].Detail != "item 42" || rv.PostProcessing[4].Detail == "" {
		t.Errorf("details for a user %+v, an admin %+v", user.PostProcessing, rv.PostProcessing)
	}
}

// A run that never recorded its end (the process died in it) still counts: at the limit the
// step is failed, not run again.
func TestPostStepThatKeepsCrashing(t *testing.T) {
	x := newImportEnv(t)
	step := &recordingStep{name: "s"}
	x.eng.AddPostStep(step)
	j := x.postJob()
	if err := x.st.PutPostStep(x.ctx, store.PostStep{JobID: j.ID, Name: "s", Status: store.StepPending, Attempts: x.cfg.Server.PostStepAttempts, UpdatedAt: x.clock.now()}); err != nil {
		t.Fatal(err)
	}
	if j, _ = x.step(x.eng, j); j.State != store.Done || step.count() != 0 || x.steps(j)["s"].Status != store.StepFailed {
		t.Fatalf("job %s, %d runs, %+v", j.State, step.count(), x.steps(j)["s"])
	}
}

// The runner step runs the steps in a goroutine of their own and returns at once; a second
// pass while it runs does not start another.
func TestPostProcessingRunsInTheBackground(t *testing.T) {
	x := newImportEnv(t)
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	step := &recordingStep{name: "slow", answer: func(int) (bool, string, error) {
		started <- struct{}{}
		<-release
		return true, "", nil
	}}
	x.eng.AddPostStep(step)
	j := x.postJob()
	if err := x.eng.PostProcessing(x.ctx, j); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := x.eng.PostProcessing(x.ctx, j); err != nil {
		t.Fatal(err)
	}
	close(release)
	x.eng.Wait()
	if step.count() != 1 || x.jobNow(j).State != store.Done {
		t.Fatalf("%d runs, job %s", step.count(), x.jobNow(j).State)
	}
}

type linkStep struct{ recordingStep }

func (*linkStep) Link(detail string) (string, bool) {
	id, ok := strings.CutPrefix(detail, "item ")
	return "https://media.example.com/web/#/details?id=" + id, ok
}

// A step that is a Linker gives the result its title link once it is ok.
func TestTitleLink(t *testing.T) {
	x := newImportEnv(t)
	x.eng.AddPostStep(&linkStep{recordingStep{name: "mediaServerRefresh", answer: func(int) (bool, string, error) { return true, "item 7", nil }}})
	j := x.postJob()
	if _, rv := x.detail(j); rv.TitleLink != nil {
		t.Fatalf("link before the step: %v", *rv.TitleLink)
	}
	j = x.drive(x.eng, j)
	if _, rv := x.detail(j); rv.TitleLink == nil || *rv.TitleLink != "https://media.example.com/web/#/details?id=7" {
		t.Fatalf("link %v", rv.TitleLink)
	}
}

func TestSkipAndGiveUp(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", Skip("off"))
	if r, ok := SkipReason(err); !ok || r != "off" || !errors.Is(err, ErrStepSkipped) || errors.Is(err, ErrStepGaveUp) {
		t.Errorf("skip: %v %q", ok, r)
	}
	base := errors.New("x")
	g := GiveUp(base)
	if !errors.Is(g, ErrStepGaveUp) || !errors.Is(g, base) || g.Error() != "x" || GiveUp(nil) != nil {
		t.Errorf("give up: %v", g)
	}
	if _, ok := SkipReason(g); ok {
		t.Error("a failure is not a skip")
	}
}

package subtitles_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/config"
	"github.com/segun-io/fukku/internal/flows"
	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/subtitles"
	"github.com/segun-io/fukku/internal/subtitles/subtitlestest"
)

const jobID = "0123456789abcdef0123456789abcdef"

type fileList []store.File

func (l fileList) Files(context.Context, string) ([]store.File, error) { return l, nil }

type harness struct {
	t      *testing.T
	root   string
	files  fileList
	rows   []store.Row
	fake   *subtitlestest.Fake
	step   *subtitles.Step
	target store.Target
	sleeps []time.Duration
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, root: t.TempDir(), fake: &subtitlestest.Fake{}, target: store.Target{App: "radarr", TitleID: 7}}
	cfg := config.Defaults().Subtitles
	cfg.Provider = "bazarr"
	h.step = &subtitles.Step{
		Manager: h.fake, Store: &h.files, IntakeRoot: h.root, Config: cfg,
		Sleep: func(_ context.Context, d time.Duration) error { h.sleeps = append(h.sleeps, d); return nil },
	}
	return h
}

// add puts a file in the job's directory and the file list; videos also get an imported row.
func (h *harness) add(name string, kind store.FileKind, content string) store.File {
	h.t.Helper()
	rel := "files/" + name
	full := filepath.Join(h.root, "jobs", jobID, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	f := store.File{ID: int64(len(h.files) + 1), JobID: jobID, Path: rel, Kind: kind, Status: store.FileOK}
	h.files = append(h.files, f)
	if kind == store.KindVideo {
		proposal, _ := json.Marshal(arr.Row{EpisodeIDs: []int{501, 502}})
		h.rows = append(h.rows, store.Row{ID: "r" + name, JobID: jobID, FileID: f.ID, Result: store.ResultImported, Proposal: proposal})
	}
	return f
}

func (h *harness) run() (bool, string, error) {
	h.t.Helper()
	return h.step.Run(context.Background(), store.Job{ID: jobID}, h.target, h.rows)
}

func TestLanguageAndFlagsFromFileNames(t *testing.T) {
	cases := []struct {
		name         string
		lang         string
		forced, hi   bool
		wantFailText string
	}{
		{"Film (2020).es.srt", "es", false, false, ""},
		{"Film (2020).es.forced.srt", "es", true, false, ""},
		{"Film (2020).en.hi.srt", "en", false, true, ""},
		{"Film (2020).forced.fr.srt", "fr", true, false, ""},
		{"Film (2020).eng.sdh.srt", "en", false, true, ""},
		{"Film (2020).ES.ass", "es", false, false, ""},
		{"Film (2020).srt", "", false, false, "language unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.add("Film (2020).mkv", store.KindVideo, "v")
			h.add(c.name, store.KindSubtitle, "1\n00:00:01,000 --> 00:00:02,000\nHola\n")
			done, _, err := h.run()
			if c.wantFailText != "" {
				if done || err == nil || !strings.Contains(err.Error(), c.wantFailText) {
					t.Fatalf("done=%v err=%v, want failure %q", done, err, c.wantFailText)
				}
				// The step retried by itself: the engine must not run it again.
				if !errors.Is(err, flows.ErrStepGaveUp) {
					t.Fatalf("err=%v does not give up", err)
				}
				if len(h.fake.Sent()) != 0 {
					t.Fatal("an unsendable file was sent")
				}
				return
			}
			if err != nil || !done {
				t.Fatalf("done=%v err=%v", done, err)
			}
			got := h.fake.Sent()
			if len(got) != 1 {
				t.Fatalf("%d uploads", len(got))
			}
			u := got[0]
			if u.Language != c.lang || u.Forced != c.forced || u.HI != c.hi || u.App != "radarr" || u.TitleID != 7 || u.EpisodeID != 0 {
				t.Errorf("upload = %+v", u.Upload)
			}
			if !strings.Contains(u.Data, "Hola") || u.Name != c.name {
				t.Errorf("name %q data %q: the file is sent as it is", u.Name, u.Data)
			}
		})
	}
}

func TestTheUIChoiceOverridesTheFileName(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	es := h.add("Film.es.srt", store.KindSubtitle, "x")
	plain := h.add("Film.srt", store.KindSubtitle, "y")
	// As the confirmation stores them (flows.SubtitleChoices): by file id.
	h.target.Options = json.RawMessage(fmt.Sprintf(`{"added": true, "subtitles": {
		"%d": {"language": "pt", "forced": true, "hearingImpaired": true},
		"%d": {"language": "fr", "forced": false, "hearingImpaired": false}}}`, es.ID, plain.ID))
	if _, _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	got := h.fake.Sent()
	if len(got) != 2 {
		t.Fatalf("%d uploads", len(got))
	}
	for _, u := range got {
		switch u.Name {
		case "Film.es.srt":
			if u.Language != "pt" || !u.Forced || !u.HI {
				t.Errorf("%+v", u.Upload)
			}
		case "Film.srt":
			if u.Language != "fr" || u.Forced || u.HI {
				t.Errorf("%+v", u.Upload)
			}
		}
	}
}

func TestSubtitleWithoutMatchingVideoIsReportedNotSent(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	h.add("Other.es.srt", store.KindSubtitle, "x")
	done, _, err := h.run()
	if done || err == nil || !strings.Contains(err.Error(), "Other.es.srt") || !strings.Contains(err.Error(), "no video") {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if n := len(h.fake.Sent()); n != 0 {
		t.Fatalf("%d uploads", n)
	}
}

func TestVideoThatWasNotImportedIsNotMatched(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	h.add("Film.es.srt", store.KindSubtitle, "x")
	h.rows[0].Result = store.ResultFailed
	if _, _, err := h.run(); err == nil || !strings.Contains(err.Error(), "not imported") {
		t.Fatalf("err = %v", err)
	}
	if len(h.fake.Sent()) != 0 {
		t.Fatal("sent")
	}
}

func TestWrittenAtOnce(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	h.add("Film.es.srt", store.KindSubtitle, "x")
	done, detail, err := h.run()
	if !done || err != nil || !strings.Contains(detail, "1 subtitle") {
		t.Fatalf("done=%v detail=%q err=%v", done, detail, err)
	}
	if len(h.fake.Sent()) != 1 {
		t.Fatalf("%d uploads", len(h.fake.Sent()))
	}
	// A repeat of the step does not send a confirmed file again.
	if done, _, err := h.run(); !done || err != nil || len(h.fake.Sent()) != 1 {
		t.Fatalf("repeat: done=%v err=%v uploads=%d", done, err, len(h.fake.Sent()))
	}
}

func TestWrittenLate(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	h.add("Film.es.srt", store.KindSubtitle, "x")
	// The first upload is accepted but never shows; the second shows at its third look.
	h.fake.WrittenAt = func(upload, poll int) bool { return upload == 2 && poll == 3 }
	done, _, err := h.run()
	if !done || err != nil {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if n := len(h.fake.Sent()); n != 2 {
		t.Fatalf("%d uploads, want 2", n)
	}
	// 20 waits of 1s for the first upload, the 5s backoff, then 3 waits of 1s.
	backoffs := 0
	for _, d := range h.sleeps {
		if d == 5*time.Second {
			backoffs++
		}
	}
	if backoffs != 1 {
		t.Errorf("sleeps %v", h.sleeps)
	}
}

func TestRetryBacksOffAndGivesUpVisibly(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	h.add("Film.es.srt", store.KindSubtitle, "x")
	h.add("Film.en.srt", store.KindSubtitle, "y")
	h.fake.WrittenAt = func(upload, poll int) bool { return false }
	done, _, err := h.run()
	if done || err == nil {
		t.Fatalf("done=%v err=%v: a file never written must fail the step", done, err)
	}
	for _, name := range []string{"Film.es.srt", "Film.en.srt"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("%q not named in %v", name, err)
		}
	}
	// Four attempts per file, each upload with the same flags.
	if n := len(h.fake.Sent()); n != 8 {
		t.Errorf("%d uploads, want 8", n)
	}
	var backoffs []time.Duration
	for _, d := range h.sleeps {
		if d >= 5*time.Second {
			backoffs = append(backoffs, d)
		}
	}
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second}
	if len(backoffs) != 6 || backoffs[0] != want[0] || backoffs[1] != want[1] || backoffs[2] != want[2] {
		t.Errorf("backoffs %v", backoffs)
	}
}

func TestRefusedUploadsAreRetried(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	h.add("Film.es.srt", store.KindSubtitle, "x")
	h.fake.UploadErr = func(n int) error {
		if n < 3 {
			return subtitles.ErrUnknownTitle
		}
		return nil
	}
	if done, _, err := h.run(); !done || err != nil {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if n := len(h.fake.Sent()); n != 3 {
		t.Fatalf("%d uploads", n)
	}
}

func TestEpisodeUploadIsForEveryEpisodeOfTheVideo(t *testing.T) {
	h := newHarness(t)
	h.target = store.Target{App: "sonarr", TitleID: 9}
	h.add("Show.S01E01E02.mkv", store.KindVideo, "v")
	h.add("Show.S01E01E02.es.srt", store.KindSubtitle, "x")
	if _, _, err := h.run(); err != nil {
		t.Fatal(err)
	}
	got := h.fake.Sent()
	if len(got) != 2 || got[0].App != "sonarr" || got[0].TitleID != 9 || got[0].EpisodeID != 501 || got[1].EpisodeID != 502 {
		t.Fatalf("%+v", got)
	}
}

func TestSkipped(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	h.add("Film.es.srt", store.KindSubtitle, "x")
	h.step.Config.Provider = "none"
	_, _, err := h.run()
	if reason, ok := flows.SkipReason(err); !ok || !errors.Is(err, flows.ErrStepSkipped) || reason != "no subtitle manager is configured" {
		t.Fatalf("provider none: err=%v", err)
	}
	h.step.Config.Provider = "bazarr"
	h.step.Config.HandOverUploads = false
	_, _, err = h.run()
	if reason, ok := flows.SkipReason(err); !ok || reason != "handing uploads over is switched off" {
		t.Fatalf("hand-over off: err=%v", err)
	}
	if len(h.fake.Sent()) != 0 {
		t.Fatal("sent")
	}
}

func TestNoSubtitleFiles(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	_, _, err := h.run()
	if reason, ok := flows.SkipReason(err); !ok || reason != "no subtitle files" {
		t.Fatalf("err=%v", err)
	}
}

func TestOnlyAcceptedRegularFilesWithinTheLimitAreRead(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	big := h.add("Film.es.srt", store.KindSubtitle, strings.Repeat("x", 100))
	h.step.Config.MaxFileSize = 50
	if _, _, err := h.run(); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("err = %v", err)
	}
	h.step.Config.MaxFileSize = 1 << 20

	// A pending file (not checked by the worker) is not touched.
	h.files[big.ID-1].Status = store.FilePending
	if _, _, err := h.run(); !errors.Is(err, flows.ErrStepSkipped) {
		t.Fatalf("pending: err=%v", err)
	}
	h.files[big.ID-1].Status = store.FileOK

	// A symlink is not followed.
	full := filepath.Join(h.root, "jobs", jobID, "files", "Film.es.srt")
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, full); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.run(); err == nil {
		t.Fatal("a symlink was read")
	}
	for _, s := range h.fake.Sent() {
		if strings.Contains(s.Data, "secret") {
			t.Fatal("leaked")
		}
	}
}

// The worker writes the job's files directory and is assumed compromised (threat model
// B4): a symlinked directory on the way to an accepted subtitle file must not make the web
// process read a file outside the job directory, such as its own secrets, and hand it to
// the subtitle manager (#63).
func TestASymlinkedDirectoryIsNotFollowed(t *testing.T) {
	h := newHarness(t)
	h.add("Season/Film.mkv", store.KindVideo, "v")
	h.add("Season/Film.es.srt", store.KindSubtitle, "1\n00:00:01,000 --> 00:00:02,000\nhola\n")

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "Film.es.srt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.root, "jobs", jobID, "files", "Season")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.run(); err == nil {
		t.Fatal("a file behind a symlinked directory was read")
	}
	for _, s := range h.fake.Sent() {
		if strings.Contains(s.Data, "secret") {
			t.Fatal("leaked a file from outside the job directory")
		}
	}
}

// os.Root follows a link that stays inside the job directory; the hand-over refuses it
// anyway, since only a regular file the worker accepted may be read (#63).
func TestALinkInsideTheJobDirectoryIsNotFollowed(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	h.add("Film.es.srt", store.KindSubtitle, "1\n00:00:01,000 --> 00:00:02,000\nhola\n")
	job := filepath.Join(h.root, "jobs", jobID)
	if err := os.MkdirAll(filepath.Join(job, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job, "work", "x"), []byte("not the subtitle"), 0o644); err != nil {
		t.Fatal(err)
	}
	srt := filepath.Join(job, "files", "Film.es.srt")
	if err := os.Remove(srt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../work/x", srt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.run(); err == nil {
		t.Fatal("a link inside the job directory was followed")
	}
	for _, s := range h.fake.Sent() {
		if strings.Contains(s.Data, "not the subtitle") {
			t.Fatal("sent the link's target")
		}
	}
}

func TestContextCancelStopsTheStep(t *testing.T) {
	h := newHarness(t)
	h.add("Film.mkv", store.KindVideo, "v")
	h.add("Film.es.srt", store.KindSubtitle, "x")
	ctx, cancel := context.WithCancel(context.Background())
	h.step.Sleep = func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }
	h.fake.WrittenAt = func(int, int) bool { return false }
	_, _, err := h.step.Run(ctx, store.Job{ID: jobID}, h.target, h.rows)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// With the season-dub policy subtitlesOnly (#57) nothing was imported: the subtitles of the
// rows that have a file in the library go to the manager, those of any other episode are
// reported, and nothing is sent for them.
func TestSubtitlesOnlyGoesToTheEpisodesThatHaveAFile(t *testing.T) {
	h := newHarness(t)
	h.target = store.Target{App: "sonarr", TitleID: 9, Options: json.RawMessage(`{"seasonDub":{"policy":"subtitlesOnly","seasons":[1],"rows":["rShow.S01E01.mkv"]}}`)}
	h.add("Show.S01E01.mkv", store.KindVideo, "v")
	h.add("Show.S01E02.mkv", store.KindVideo, "v")
	h.add("Show.S01E01.es.srt", store.KindSubtitle, "x")
	for i := range h.rows { // skipped, as the confirmation leaves them
		h.rows[i].Skip, h.rows[i].Result = true, store.ResultSkipped
	}
	if _, _, err := h.run(); err != nil {
		t.Fatalf("the listed row: %v", err)
	}
	if got := h.fake.Sent(); len(got) != 2 || got[0].EpisodeID != 501 {
		t.Fatalf("sent %+v", got)
	}

	h.add("Show.S01E02.es.srt", store.KindSubtitle, "y")
	if _, _, err := h.run(); err == nil || !strings.Contains(err.Error(), "Show.S01E02.es.srt") || !strings.Contains(err.Error(), "no file in the library") {
		t.Fatalf("an episode without a file: %v", err)
	}
	// A row the user left out says so, not that its episode has no file.
	for i := range h.rows {
		if h.rows[i].ID != "rShow.S01E01.mkv" {
			h.rows[i].Edit = json.RawMessage(`{"skip":true}`)
		}
	}
	if _, _, err := h.run(); err == nil || !strings.Contains(err.Error(), "left out by the user") {
		t.Fatalf("a row the user left out: %v", err)
	}
	for i := range h.rows {
		h.rows[i].Edit = nil
	}
	if n := len(h.fake.Sent()); n != 2 {
		t.Errorf("%d uploads, want only the first file's", n)
	}

	// Episodes kept as second versions (#58): their subtitles are not handed over, and that is
	// not a failure.
	h.target.Options = json.RawMessage(`{"seasonDub":{"policy":"version"},"episodeVersions":{"label":"Español","versions":[{"row":"rShow.S01E01.mkv","path":"/tv/x.mkv"},{"row":"rShow.S01E02.mkv","path":"/tv/y.mkv"}]}}`)
	// (The first file was handed over by the run above.)
	if _, detail, err := h.run(); err != nil || !strings.Contains(detail, "1 subtitle file(s) not handed over") {
		t.Fatalf("versions: %q, %v", detail, err)
	}
	if n := len(h.fake.Sent()); n != 2 {
		t.Errorf("%d uploads, want none more", n)
	}

	// Without the policy a skipped row is never given subtitles.
	h.target.Options = nil
	if _, _, err := h.run(); err == nil || !strings.Contains(err.Error(), "not imported") {
		t.Fatalf("without the policy: %v", err)
	}
}

// A merge (#55) imports the merged file, not the upload: the upload's subtitle files are
// handed over for the film all the same.
func TestMergeHandsTheUploadsSubtitlesOver(t *testing.T) {
	h := newHarness(t)
	up := h.add("Film.mkv", store.KindVideo, "v")
	h.add("Film.es.srt", store.KindSubtitle, "x")
	merged := store.File{ID: 10, JobID: jobID, Path: "merged/1/Film (2001) WEBDL-1080p.mkv", Kind: store.KindVideo, Status: store.FileOK}
	h.files = append(h.files, merged)
	proposal, _ := json.Marshal(arr.Row{MovieID: 7})
	h.rows = []store.Row{{ID: "r10", JobID: jobID, FileID: merged.ID, Result: store.ResultImported, Proposal: proposal}}
	// Without the merge, the upload was not imported.
	if _, _, err := h.run(); err == nil || !strings.Contains(err.Error(), "not imported") {
		t.Fatalf("without the merge: err = %v", err)
	}
	h.target.Options = json.RawMessage(fmt.Sprintf(`{"merge": {"uploadFileId": %d, "upload": %q, "output": {"fileId": 10, "path": %q, "size": 1}}}`,
		up.ID, up.Path, merged.Path))
	done, _, err := h.run()
	if !done || err != nil || len(h.fake.Sent()) != 1 {
		t.Fatalf("done=%v err=%v uploads=%d", done, err, len(h.fake.Sent()))
	}
	if s := h.fake.Sent()[0]; s.App != "radarr" || s.TitleID != 7 || s.Language != "es" {
		t.Errorf("sent %+v", s)
	}
}

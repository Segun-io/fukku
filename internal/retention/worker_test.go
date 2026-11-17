package retention_test

import (
	"os"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
)

// A name the worker made in its own folder of a file in a job (a hard link, through a
// descriptor it kept) goes at the next sweep, with every entry of worker/ that is no job's
// folder; the worker's folder of a job that is done goes too. The worker's scratch work of
// a running job stays (#114).
func TestSweepKeepsTheWorkersFolderToItsScratchWork(t *testing.T) {
	e := newEnv(t)
	running := e.jobIn(store.Probed, e.clk.Now().Add(jobRetention))
	done := e.jobIn(store.Done, time.Time{})
	write(t, e.path("jobs", running.ID, "files", "a.mkv"), "upload", time.Time{})
	write(t, e.path("worker", running.ID, "work", "previews", "c1.mp4"), "clip", time.Time{})
	if err := os.Link(e.path("jobs", running.ID, "files", "a.mkv"), e.path("worker", running.ID, "work", "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(e.path("jobs", running.ID, "files", "a.mkv"), e.path("worker", "kept")); err != nil {
		t.Fatal(err)
	}
	write(t, e.path("worker", done.ID, "work", "x"), "x", time.Time{})

	e.run()

	if e.exists("worker", running.ID, "work", "alias") || e.exists("worker", "kept") {
		t.Error("a second name of the job's file is left in worker/")
	}
	if !e.exists("worker", running.ID, "work", "previews", "c1.mp4") {
		t.Error("the running job's clip was removed")
	}
	if e.exists("worker", done.ID) {
		t.Error("the worker's folder of a job that is done is left")
	}
	if !e.exists("jobs", running.ID, "files", "a.mkv") {
		t.Error("the job's file was removed")
	}
}

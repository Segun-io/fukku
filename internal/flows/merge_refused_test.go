package flows

import (
	"testing"

	"github.com/segun-io/fukku/internal/merge"
	"github.com/segun-io/fukku/internal/store"
)

// A merge refused for a reason the same choice would meet again (#95) goes back to
// awaiting confirmation with the refusal and the preview kept, is audited as merge.refused,
// leaves nothing of the attempt behind and never touches the library; a second confirmation
// with another offset merges, and the job ends done with one import.
func TestMergeRepeatableRefusalGoesBackToTheConfirmation(t *testing.T) {
	for _, code := range []string{merge.RefusalVerifyFailed, merge.RefusalMuxFailed, merge.RefusalTrackNotFound} {
		t.Run(code, func(t *testing.T) {
			x := newMergeEnv(t)
			j := x.confirmMerge(x.readyJob(), nil)
			before, _ := PreviewOf(x.tgt(j))
			refuse := func(o *merge.Output) { *o = merge.Output{Refusal: code, Detail: "technical detail"} }
			if j = x.run(x.eng, j, refuse); j.State != store.AwaitingConfirmation || j.ErrorCode != "" {
				t.Fatalf("job %s %s (%s), want awaiting confirmation", j.State, j.ErrorCode, j.ErrorText)
			}
			x.untouched()

			// The refusal is kept with the choice it refused; the choice is gone, the preview stays.
			tg := x.tgt(j)
			if IsMerge(tg) {
				t.Error("the refused choice is still the job's merge")
			}
			r, ok := mergeRefusalOf(tg)
			if !ok || r.Code != code || r.OffsetMs != -800 || len(r.AudioTracks) != 1 || r.AudioTracks[0] != 2 || r.Attempt != j.Attempt {
				t.Errorf("refusal %+v", r)
			}
			if after, ok := PreviewOf(tg); !ok || after.State != PreviewReady || after.Request != before.Request || len(after.Clips) == 0 {
				t.Errorf("preview %+v, was %+v", after, before)
			}
			files, _ := x.st.Files(x.ctx, j.ID)
			v := ViewFlow2(x.cfg, tg, files)
			if v == nil || v.MergeRefusal == nil || v.MergeRefusal.Code != code || v.MergeRefusal.Message == "" || v.MergeRefusal.OffsetMs != -800 {
				t.Errorf("flow 2 view %+v", v)
			}

			// Audited as merge.refused; the refused attempt's directory and task are gone.
			var refused []store.AuditEntry
			log, _ := x.st.AuditLog(x.ctx, j.ID)
			for _, e := range log {
				if e.Action == "merge.refused" {
					refused = append(refused, e)
				}
			}
			if len(refused) != 1 || refused[0].Detail["refusal"] != code || refused[0].Detail["offsetMs"] != "-800" {
				t.Errorf("audit %+v", refused)
			}
			if left := x.mergedOnDisk(j); len(left) != 0 {
				t.Errorf("left behind on disk: %v", left)
			}
			if tasks := x.q.Tasks(); len(tasks) != 0 {
				t.Errorf("tasks left %+v", tasks)
			}
			// The passes of the runner leave the job alone (no merge is submitted again).
			x.pass(j)
			if got := x.jobNow(j); got.State != store.AwaitingConfirmation || len(x.q.Tasks()) != 0 {
				t.Fatalf("job %s, tasks %+v", got.State, x.q.Tasks())
			}
			// The same offset and track can be confirmed again as they are, but another
			// offset needs its own preview first.
			x.preview(j, -600)
			ms := int64(-600)
			j = x.confirmMerge(j, &MergeRequest{OffsetMs: &ms})
			if _, ok := mergeRefusalOf(x.tgt(j)); ok {
				t.Error("the refusal outlived the next confirmation")
			}
			if c := x.choice(j); c.OffsetMs != -600 {
				t.Errorf("choice %+v", c)
			}
			p := x.propose(j, j.Attempt)
			if j = x.run(x.eng, j, nil); j.State != store.Done {
				t.Fatalf("job %s (%s)", j.State, j.ErrorText)
			}
			if len(x.movies.Imports) != 1 || x.movies.Imports[0].Rows[0].Path != p {
				t.Fatalf("imports %+v", x.movies.Imports)
			}
			x.noMergeLeftovers(j)
		})
	}
}

// A refusal that may pass stays a failure the owner retries, as before: free space, a
// library or upload that cannot be read, a destination that is busy is not asked again.
func TestMergeTransientRefusalsStayFailures(t *testing.T) {
	for _, code := range []string{merge.RefusalFreeSpace, merge.RefusalLibraryUnreadable, merge.RefusalUploadUnreadable, merge.RefusalOutsideLibrary} {
		t.Run(code, func(t *testing.T) {
			x := newMergeEnv(t)
			j := x.confirmMerge(x.readyJob(), nil)
			refuse := func(o *merge.Output) { *o = merge.Output{Refusal: code, Detail: "detail"} }
			if j = x.run(x.eng, j, refuse); j.State != store.Failed || j.ErrorCode != CodeMergeFailed {
				t.Fatalf("job %s %s", j.State, j.ErrorCode)
			}
			if _, ok := mergeRefusalOf(x.tgt(j)); ok {
				t.Error("a transient refusal was kept for the confirmation")
			}
			if v := x.importView(j); v.Merge == nil || v.Merge.Refusal != code {
				t.Errorf("merge view %+v", v.Merge)
			}
		})
	}
}

// The refusal is answered by the next confirmation, whatever it is; a job that is refused
// again goes back again, with the new refusal.
func TestMergeRefusedAgain(t *testing.T) {
	x := newMergeEnv(t)
	j := x.confirmMerge(x.readyJob(), nil)
	refuse := func(code string) func(*merge.Output) {
		return func(o *merge.Output) { *o = merge.Output{Refusal: code, Detail: "detail"} }
	}
	if j = x.run(x.eng, j, refuse(merge.RefusalVerifyFailed)); j.State != store.AwaitingConfirmation {
		t.Fatalf("job %s", j.State)
	}
	j = x.confirmMerge(j, nil) // the same choice again
	if j = x.run(x.eng, j, refuse(merge.RefusalMuxFailed)); j.State != store.AwaitingConfirmation {
		t.Fatalf("job %s (%s)", j.State, j.ErrorText)
	}
	if r, ok := mergeRefusalOf(x.tgt(j)); !ok || r.Code != merge.RefusalMuxFailed {
		t.Errorf("refusal %+v", r)
	}
	n := 0
	log, _ := x.st.AuditLog(x.ctx, j.ID)
	for _, e := range log {
		if e.Action == "merge.refused" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("%d merge.refused entries, want 2", n)
	}
	x.untouched()
}

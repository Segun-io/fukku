package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
	"github.com/segun-io/fukku/internal/store/storetest"
)

// mergeJob is a job of user1 that was confirmed as a merge (#55) and is in state, with the
// import record given (none when imp is nil).
func (e *confirmEnv) mergeJob(state store.State, imp *store.Import) store.Job {
	e.t.Helper()
	ctx := e.t.Context()
	j, err := e.st.CreateJob(ctx, "user1")
	if err != nil {
		e.t.Fatal(err)
	}
	if j, err = storetest.Drive(ctx, e.st, j.ID, store.Importing); err != nil {
		e.t.Fatal(err)
	}
	tgt := store.Target{App: "radarr", TitleID: 7, Title: "Some Film", Year: 2001,
		Options: json.RawMessage(`{"merge":{"upload":"files/Some Film.es.mkv"}}`)}
	if err := e.st.SetTarget(ctx, j.ID, tgt); err != nil {
		e.t.Fatal(err)
	}
	if imp != nil {
		imp.JobID = j.ID
		if err := e.st.PutImport(ctx, *imp); err != nil {
			e.t.Fatal(err)
		}
	}
	if state == store.Stalled {
		if j, err = e.st.Transition(ctx, j.ID, store.Importing, store.Stalled, store.Change{Actor: "system", ErrorCode: "worker_unavailable"}); err != nil {
			e.t.Fatal(err)
		}
	}
	return j
}

func (e *confirmEnv) offered(user, groups string, j store.Job) bool {
	e.t.Helper()
	rec := e.send(user, groups, "GET", "/api/jobs/"+j.ID, "", false)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("detail: %d %s", rec.Code, rec.Body)
	}
	return strings.Contains(rec.Body.String(), `"cancel"`)
}

// While no import is submitted, the owner cancels a merge job (also one stalled in the
// merge): the audit entry is job.cancelled, not import.abandoned.
func TestMergeJobCancelledBeforeTheImport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state store.State
		imp   *store.Import
	}{
		{"importing, no import record", store.Importing, nil},
		{"importing, record not submitted", store.Importing, &store.Import{}},
		{"stalled in the merge", store.Stalled, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newConfirmEnv(t)
			j := e.mergeJob(tc.state, tc.imp)
			if !e.offered("user1", "users", j) {
				t.Error("cancel not offered to the owner")
			}
			// Somebody else's job is not found.
			e.want(e.send("user2", "users", "POST", "/api/jobs/"+j.ID+"/cancel", ``, true), http.StatusNotFound, "not_found")
			if rec := e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/cancel", ``, true); rec.Code != http.StatusOK {
				t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
			}
			if got, _ := e.st.Job(t.Context(), j.ID); got.State != store.Cancelled {
				t.Fatalf("job %s", got.State)
			}
			log, _ := e.st.AuditLog(t.Context(), j.ID)
			if last := log[len(log)-1]; last.Action != "job.cancelled" || last.Actor != "user1" || last.Detail["from"] != string(tc.state) || last.Detail["to"] != "cancelled" {
				t.Errorf("audit %+v", last)
			}
			// Once cancelled, it cannot be cancelled again.
			e.want(e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/cancel", ``, true), http.StatusConflict, "wrong_state")
		})
	}
}

// An admin may cancel it too, with no wait for server.jobStuckAfter.
func TestAdminCancelsAMergeBeforeTheImport(t *testing.T) {
	e := newConfirmEnv(t)
	j := e.mergeJob(store.Importing, nil)
	if !e.offered("admin1", "admins", j) {
		t.Fatal("cancel not offered to an admin")
	}
	if rec := e.send("admin1", "admins", "POST", "/api/jobs/"+j.ID+"/cancel", ``, true); rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	log, _ := e.st.AuditLog(t.Context(), j.ID)
	if last := log[len(log)-1]; last.Action != "job.cancelled" || last.Actor != "admin1" {
		t.Errorf("audit %+v", last)
	}
}

// Once the import is submitted the rules of any import apply: the owner is refused, and an
// admin may abandon it only after server.jobStuckAfter (import.abandoned).
func TestMergeJobCannotBeCancelledAfterTheSubmission(t *testing.T) {
	for _, state := range []store.State{store.Importing, store.Stalled} {
		t.Run(string(state), func(t *testing.T) {
			e := newConfirmEnv(t)
			j := e.mergeJob(state, &store.Import{CommandID: 4321, SubmittedAt: e.clock.now()})
			path := "/api/jobs/" + j.ID
			if state == store.Importing {
				e.clock.advance(59 * time.Minute)
				for _, who := range [][2]string{{"user1", "users"}, {"admin1", "admins"}} {
					if e.offered(who[0], who[1], j) {
						t.Errorf("cancel offered to %s before server.jobStuckAfter", who[0])
					}
					e.want(e.send(who[0], who[1], "POST", path+"/cancel", ``, true), http.StatusConflict, "wrong_state")
				}
				e.clock.advance(2 * time.Minute)
			}
			if e.offered("user1", "users", j) {
				t.Error("cancel offered to the owner")
			}
			e.want(e.send("user1", "users", "POST", path+"/cancel", ``, true), http.StatusConflict, "wrong_state")
			if !e.offered("admin1", "admins", j) {
				t.Fatal("cancel not offered to an admin")
			}
			if rec := e.send("admin1", "admins", "POST", path+"/cancel", ``, true); rec.Code != http.StatusOK {
				t.Fatalf("admin cancel: %d %s", rec.Code, rec.Body)
			}
			log, _ := e.st.AuditLog(t.Context(), j.ID)
			if last := log[len(log)-1]; last.Action != "import.abandoned" || last.Detail["command"] != "4321" {
				t.Errorf("audit %+v", last)
			}
		})
	}
}

// A job that is not a merge keeps the rules of any import: no import record yet does not
// let its owner cancel it.
func TestOnlyAMergeCancelsBeforeTheImport(t *testing.T) {
	e := newConfirmEnv(t)
	j := e.mergeJob(store.Importing, nil)
	if err := e.st.SetTarget(t.Context(), j.ID, store.Target{App: "radarr", TitleID: 7, Title: "Some Film", Year: 2001}); err != nil {
		t.Fatal(err)
	}
	if e.offered("user1", "users", j) {
		t.Error("cancel offered for an importing job that is not a merge")
	}
	e.want(e.send("user1", "users", "POST", "/api/jobs/"+j.ID+"/cancel", ``, true), http.StatusConflict, "wrong_state")
}

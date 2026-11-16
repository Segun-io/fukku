package storetest

import (
	"errors"
	"testing"
	"time"

	"github.com/segun-io/fukku/internal/store"
)

// versionTests are the conformance tests of store.Versions (#56); Run includes them.
var versionTests = []struct {
	name string
	fn   func(t *testing.T, s store.Store)
}{
	{"Versions", testVersions},
	{"VersionsOutliveJobs", testVersionsOutliveJobs},
	{"ReplaceTargetFromImporting", testReplaceTargetFromImporting},
}

func testVersions(t *testing.T, s store.Store) {
	ctx := t.Context()
	if all := must(s.AllVersions(ctx))(t); len(all) != 0 {
		t.Fatalf("a new store lists %d versions", len(all))
	}
	if _, err := s.VersionByPath(ctx, "/data/versions/Film (2001)/Film (2001) - Español.mkv"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unknown path: %v, want ErrNotFound", err)
	}
	if _, err := s.PutVersion(ctx, store.Version{JobID: "j1", App: "radarr", TitleID: 1}); err == nil {
		t.Fatal("a version without a path was stored")
	}
	v := store.Version{
		JobID: "j1", App: "radarr", TitleID: 603, Title: "Some Film", Year: 2001,
		Path: "/data/versions/Some Film (2001)/Some Film (2001) - Español.mkv", Label: "Español", Status: store.VersionPlacing,
	}
	a := must(s.PutVersion(ctx, v))(t)
	if a.ID == 0 || a.CreatedAt.IsZero() || a.Status != store.VersionPlacing || a.Label != "Español" || !a.CheckedAt.IsZero() {
		t.Fatalf("inserted %+v", a)
	}
	if _, err := s.PutVersion(ctx, v); !errors.Is(err, store.ErrExists) {
		t.Fatalf("a second record for the same path: %v, want ErrExists", err)
	}
	b := must(s.PutVersion(ctx, store.Version{JobID: "j2", App: "radarr", TitleID: 7, Path: "/data/versions/Other (1999)/Other (1999) - Deutsch.mkv", Status: store.VersionLinked}))(t)
	if b.ID == a.ID {
		t.Fatalf("two records with id %d", a.ID)
	}

	// Replace: every field but CreatedAt is written.
	at := time.Date(2026, 5, 6, 7, 8, 9, 10, time.UTC)
	upd := a
	upd.Status, upd.Detail = store.VersionGrouped, ""
	upd.PrimaryFileID, upd.PrimaryPath, upd.PrimaryItemID, upd.ItemID = 12, "/data/media/movies/Some Film (2001)/Some Film (2001).mkv", "p1", "v1"
	upd.CheckedAt, upd.GroupedAt, upd.PrimaryChangedAt = at, at, at.Add(time.Second)
	upd.CreatedAt = at.Add(-time.Hour)
	got := must(s.PutVersion(ctx, upd))(t)
	if !got.CreatedAt.Equal(a.CreatedAt) {
		t.Fatalf("CreatedAt changed on replace: %v, want %v", got.CreatedAt, a.CreatedAt)
	}
	read := must(s.VersionByPath(ctx, a.Path))(t)
	if read.ID != a.ID || read.Status != store.VersionGrouped || read.PrimaryFileID != 12 || read.PrimaryItemID != "p1" || read.ItemID != "v1" ||
		read.PrimaryPath != upd.PrimaryPath || !read.CheckedAt.Equal(at) || !read.GroupedAt.Equal(at) ||
		!read.PrimaryChangedAt.Equal(at.Add(time.Second)) || !read.CreatedAt.Equal(a.CreatedAt) || read.Title != "Some Film" || read.Year != 2001 {
		t.Fatalf("read back %+v", read)
	}
	// Replacing with another record's path, or an unknown id, is refused and changes nothing.
	clash := read
	clash.Path = b.Path
	if _, err := s.PutVersion(ctx, clash); !errors.Is(err, store.ErrExists) {
		t.Fatalf("taking another record's path: %v, want ErrExists", err)
	}
	if _, err := s.PutVersion(ctx, store.Version{ID: 9999, Path: "/x/y.mkv"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("replacing an unknown id: %v, want ErrNotFound", err)
	}
	if again := must(s.VersionByPath(ctx, a.Path))(t); again.Status != store.VersionGrouped {
		t.Fatalf("a refused write changed the record: %+v", again)
	}

	all := must(s.AllVersions(ctx))(t)
	if len(all) != 2 || all[0].ID != a.ID || all[1].ID != b.ID {
		t.Fatalf("AllVersions %+v", all)
	}
	of := must(s.VersionsOf(ctx, "j2"))(t)
	if len(of) != 1 || of[0].ID != b.ID {
		t.Fatalf("VersionsOf(j2) %+v", of)
	}
	if none := must(s.VersionsOf(ctx, "j3"))(t); len(none) != 0 {
		t.Fatalf("VersionsOf an unknown job: %+v", none)
	}
}

// A version stays when its job is deleted: it must be grouped again for as long as it exists.
func testVersionsOutliveJobs(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := newJob(t, s, "user1")
	v := must(s.PutVersion(ctx, store.Version{JobID: j.ID, App: "radarr", TitleID: 1, Path: "/data/versions/A (2000)/A (2000) - Español.mkv", Status: store.VersionLinked}))(t)
	if err := s.DeleteJob(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	got := must(s.VersionsOf(ctx, j.ID))(t)
	if len(got) != 1 || got[0].ID != v.ID {
		t.Fatalf("after DeleteJob: %+v", got)
	}
}

// A flow 2 job whose library file changed before its version was placed goes back from
// importing to probed without its target (#56), like a stale match.
func testReplaceTargetFromImporting(t *testing.T, s store.Store) {
	ctx := t.Context()
	j := drive(t, s, newJob(t, s, "user1").ID, store.Importing)
	if err := s.SetTarget(ctx, j.ID, store.Target{App: "radarr", TitleID: 5, Title: "Film"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReplaceTarget(ctx, j.ID, store.Importing, store.Probed, nil, store.Change{Actor: "system", Note: "stale"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.Probed {
		t.Fatalf("state %s, want probed", got.State)
	}
	if _, err := s.Target(ctx, j.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("target after the move: %v, want ErrNotFound", err)
	}
	h := history(t, s, j.ID)
	if e := h[len(h)-1]; e.From != store.Importing || e.To != store.Probed || e.Note != "stale" {
		t.Fatalf("event %+v", e)
	}
	// Importing never goes to matched this way: the user matches again.
	k := drive(t, s, newJob(t, s, "user1").ID, store.Importing)
	if _, err := s.ReplaceTarget(ctx, k.ID, store.Importing, store.Matched, &store.Target{App: "radarr", TitleID: 5}, store.Change{}); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("importing → matched: %v, want ErrInvalidTransition", err)
	}
}

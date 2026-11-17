package flows

import (
	"errors"
	"testing"

	"github.com/segun-io/fukku/internal/store"
)

// A placing record stranded by a crash and then a cancelled job (second review of #89):
// SettlePlacing marks it linked when the file is the job's upload, removes it otherwise,
// and leaves the record of a job still going alone.
func TestSettlePlacing(t *testing.T) {
	// placed: a crash after the link, before the record said linked; crashed: before it.
	setup := func(t *testing.T, placed bool) (*venv, store.Job, store.Version) {
		x := newVersionEnv(t)
		j := x.versionJob(palMs, "spa")
		c, err := x.confirmVersion(j, "")
		if err != nil {
			t.Fatal(err)
		}
		choice, _ := versionOf(c.Target)
		if placed {
			if err := linkVersion(x.versions, x.src(j, "files/Some Film (2001).es.mkv"), choice.Folder, choice.Name); err != nil {
				t.Fatal(err)
			}
		}
		rec, err := x.st.PutVersion(x.ctx, store.Version{JobID: j.ID, App: AppRadarr, TitleID: filmID, Path: choice.Path, Status: store.VersionPlacing})
		if err != nil {
			t.Fatal(err)
		}
		return x, c.Job, rec
	}
	t.Run("job still going", func(t *testing.T) {
		x, _, rec := setup(t, true)
		settled, _, err := SettlePlacing(x.ctx, x.st, x.cfg.Paths.IntakeRoot, rec)
		if err != nil || settled {
			t.Fatalf("settled %t, %v", settled, err)
		}
	})
	t.Run("cancelled after the link", func(t *testing.T) {
		x, j, rec := setup(t, true)
		if _, err := x.st.Transition(x.ctx, j.ID, store.Importing, store.Cancelled, store.Change{Actor: "admin1"}); err != nil {
			t.Fatal(err)
		}
		settled, linked, err := SettlePlacing(x.ctx, x.st, x.cfg.Paths.IntakeRoot, rec)
		if err != nil || !settled || !linked {
			t.Fatalf("settled %t linked %t, %v", settled, linked, err)
		}
		got, _ := x.st.VersionByPath(x.ctx, rec.Path)
		if got.Status != store.VersionLinked {
			t.Errorf("record %+v", got)
		}
		if acts := x.actions(j); acts[len(acts)-1] != "version.linked" {
			t.Errorf("audit %v", acts)
		}
	})
	t.Run("cancelled before the link", func(t *testing.T) {
		x, j, rec := setup(t, false)
		if _, err := x.st.Transition(x.ctx, j.ID, store.Importing, store.Cancelled, store.Change{Actor: "admin1"}); err != nil {
			t.Fatal(err)
		}
		settled, linked, err := SettlePlacing(x.ctx, x.st, x.cfg.Paths.IntakeRoot, rec)
		if err != nil || !settled || linked {
			t.Fatalf("settled %t linked %t, %v", settled, linked, err)
		}
		if _, err := x.st.VersionByPath(x.ctx, rec.Path); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("the record is still there: %v", err)
		}
		// The name is free again.
		y := x.versionJob(palMs, "spa")
		if _, err := x.confirmVersion(y, ""); err != nil {
			t.Errorf("the name is still refused: %v", err)
		}
	})
}

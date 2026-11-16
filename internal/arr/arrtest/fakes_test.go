package arrtest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/segun-io/fukku/internal/arr"
	"github.com/segun-io/fukku/internal/arr/arrtest"
)

var ctx = context.Background()

func TestFakeMoviesImportLifecycle(t *testing.T) {
	f := &arrtest.FakeMovies{
		Library: []arr.Movie{{ID: 7, Title: "Big Buck Bunny", Path: "/data/media/movies/Big Buck Bunny (2008)"}},
	}
	// The Importer-side fields are promoted, so they are set after construction.
	f.Proposed = map[string][]arr.Row{"/in": {{Path: "/in/a.mkv", Size: 10}}}
	f.Verdicts = map[string][]arr.Rejection{"/in/a.mkv": {{Reason: "Not an upgrade", Permanent: true}}}
	rows, _ := f.Proposal(ctx, "/in")
	rows[0].MovieID = 7
	checked, _ := f.Recheck(ctx, rows)
	if len(checked[0].Rejections) != 1 {
		t.Errorf("verdicts = %+v", checked[0].Rejections)
	}
	id, err := f.Import(ctx, rows, "move")
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := f.Command(ctx, id); c.Status != "started" {
		t.Errorf("command = %+v", c)
	}
	f.Complete(id)
	if c, _ := f.Command(ctx, id); c.Status != "completed" || c.Result != "successful" {
		t.Errorf("command = %+v", c)
	}
	m, _ := f.Movie(ctx, 7)
	if m.File == nil || m.File.Path != "/data/media/movies/Big Buck Bunny (2008)/a.mkv" {
		t.Errorf("file = %+v", m.File)
	}
	id2, _ := f.Import(ctx, rows, "copy")
	f.Fail(id2, "disk full")
	if c, _ := f.Command(ctx, id2); c.Status != "failed" || c.Message != "disk full" {
		t.Errorf("command = %+v", c)
	}
	if cs, _ := f.Commands(ctx); len(cs) != 2 {
		t.Errorf("commands = %+v", cs)
	}
}

func TestFakeMoviesLibrary(t *testing.T) {
	f := &arrtest.FakeMovies{Results: []arr.Movie{{TMDBID: 1, Title: "A", Year: 2000}}, Queue: map[int]bool{1001: true}}
	m, err := f.Add(ctx, arr.AddMovie{TMDBID: 1, RootFolder: "/m", Monitored: true})
	if err != nil || m.ID != 1001 || m.Path != "/m/A (2000)" {
		t.Fatalf("Add = %+v, %v", m, err)
	}
	if busy, _ := f.Busy(ctx, 1001); !busy {
		t.Error("expected busy")
	}
	if err := f.SetMonitored(ctx, 1001, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.Movie(ctx, 1001); got.Monitored {
		t.Error("still monitored")
	}
	if _, err := f.Movie(ctx, 5); !errors.Is(err, arrtest.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
	f.Err = map[string]error{"Busy": errors.New("down")}
	if _, err := f.Busy(ctx, 1); err == nil {
		t.Error("expected the scripted error")
	}
}

func TestFakeSeries(t *testing.T) {
	f := &arrtest.FakeSeries{
		Results: []arr.Series{{TVDBID: 9, Title: "Show", Year: 2011, Seasons: []int{1}}},
		Eps:     map[int][]arr.Episode{2001: {{ID: 1, SeasonNumber: 1, Number: 1, Monitored: true}}},
	}
	s, err := f.Add(ctx, arr.AddSeries{TVDBID: 9, RootFolder: "/tv", Type: "standard"})
	if err != nil || s.ID != 2001 || s.Path != "/tv/Show" {
		t.Fatalf("Add = %+v, %v", s, err)
	}
	if err := f.SetEpisodesMonitored(ctx, []int{1}, false); err != nil {
		t.Fatal(err)
	}
	id, _ := f.Import(ctx, []arr.Row{{Path: "/in/e.mkv", SeriesID: 2001, EpisodeIDs: []int{1}}}, "move")
	f.Complete(id)
	eps, _ := f.Episodes(ctx, 2001)
	if eps[0].Monitored || eps[0].FileID == 0 {
		t.Errorf("episode = %+v", eps[0])
	}
	id, _ = f.Import(ctx, nil, "move")
	f.Fail(id, "x")
	if c, _ := f.Command(ctx, id); c.Status != "failed" {
		t.Errorf("command = %+v", c)
	}
}

// Package arrtest has in-memory fakes of the arr interfaces, for the tests of code that uses
// them. A fake holds a small scripted library; set its fields before use and drive commands
// with Complete and Fail.
package arrtest

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"sync"

	"github.com/segun-io/fukku/internal/arr"
)

var (
	_ arr.MovieManager  = (*FakeMovies)(nil)
	_ arr.NameParser    = (*FakeMovies)(nil)
	_ arr.SeriesManager = (*FakeSeries)(nil)
)

// ErrNotFound is returned for an id the fake does not know.
var ErrNotFound = errors.New("arrtest: not found")

// base is what the two fakes share: the Importer side.
type base struct {
	mu sync.Mutex

	// Err maps a method name ("Proposal", "Import", "Busy", ...) to an error that call returns.
	Err map[string]error

	Info     arr.Status
	Profile  []arr.Profile
	Roots    []arr.RootFolder
	Proposed map[string][]arr.Row // by folder
	// Verdicts maps a file path to the rejections Recheck reports for it (none by default).
	Verdicts map[string][]arr.Rejection
	Quals    []arr.Quality  // what Qualities returns
	Langs    []arr.Language // what Languages returns

	folders  []string    // the folder of every Proposal call
	rechecks [][]arr.Row // the rows of every Recheck call

	cmds     []arr.Command
	imported map[int][]arr.Row // rows of each import command
	// Imports records every Import call: its rows and mode.
	Imports []ImportCall
	// OnImport, when set, is called for every row an import command gives a file (Complete,
	// FailPartway), as the app moves it: tests remove the source there. It runs with the
	// fake locked, so it must not call the fake.
	OnImport func(r arr.Row)
}

// ImportCall is one recorded Import call.
type ImportCall struct {
	CommandID int
	Rows      []arr.Row
	Mode      string
}

func (b *base) fail(method string) error { return b.Err[method] }

func (b *base) Status(context.Context) (arr.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Info, b.fail("Status")
}

func (b *base) Profiles(context.Context) ([]arr.Profile, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]arr.Profile(nil), b.Profile...), b.fail("Profiles")
}

func (b *base) RootFolders(context.Context) ([]arr.RootFolder, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]arr.RootFolder(nil), b.Roots...), b.fail("RootFolders")
}

func (b *base) Qualities(context.Context) ([]arr.Quality, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]arr.Quality(nil), b.Quals...), b.fail("Qualities")
}

func (b *base) Languages(context.Context) ([]arr.Language, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]arr.Language(nil), b.Langs...), b.fail("Languages")
}

// ProposalCalls returns the folder of every Proposal call so far, failed ones included.
// Proposal takes nothing else: no title id can reach the app.
func (b *base) ProposalCalls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.folders...)
}

// RecheckCalls returns the rows of every Recheck call so far, failed ones included.
func (b *base) RecheckCalls() [][]arr.Row {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([][]arr.Row, len(b.rechecks))
	for i, rows := range b.rechecks {
		out[i] = append([]arr.Row(nil), rows...)
	}
	return out
}

func (b *base) Proposal(_ context.Context, folder string) ([]arr.Row, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.folders = append(b.folders, folder)
	if err := b.fail("Proposal"); err != nil {
		return nil, err
	}
	return append([]arr.Row(nil), b.Proposed[folder]...), nil
}

func (b *base) Recheck(_ context.Context, rows []arr.Row) ([]arr.Row, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rechecks = append(b.rechecks, append([]arr.Row(nil), rows...))
	if err := b.fail("Recheck"); err != nil {
		return nil, err
	}
	out := make([]arr.Row, len(rows))
	for i, r := range rows {
		r.Rejections = b.Verdicts[r.Path]
		out[i] = r
	}
	return out, nil
}

// Import records the call and starts a command that stays "started" until Complete or Fail.
func (b *base) Import(_ context.Context, rows []arr.Row, mode string) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.fail("Import"); err != nil {
		return 0, err
	}
	id := len(b.cmds) + 1
	cmd := arr.Command{ID: id, Name: "ManualImport", Status: "started", Result: "unknown"}
	for _, r := range rows {
		cmd.Files = append(cmd.Files, r.Path)
	}
	b.cmds = append(b.cmds, cmd)
	if b.imported == nil {
		b.imported = map[int][]arr.Row{}
	}
	b.imported[id] = append([]arr.Row(nil), rows...)
	b.Imports = append(b.Imports, ImportCall{CommandID: id, Rows: append([]arr.Row(nil), rows...), Mode: mode})
	return id, nil
}

func (b *base) Command(_ context.Context, id int) (arr.Command, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.fail("Command"); err != nil {
		return arr.Command{}, err
	}
	if id < 1 || id > len(b.cmds) {
		return arr.Command{}, fmt.Errorf("command %d: %w", id, ErrNotFound)
	}
	return copyCommand(b.cmds[id-1]), nil
}

func (b *base) Commands(context.Context) ([]arr.Command, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]arr.Command, 0, len(b.cmds))
	for _, c := range b.cmds {
		out = append(out, copyCommand(c))
	}
	return out, b.fail("Commands")
}

func copyCommand(c arr.Command) arr.Command {
	c.Files = append([]string(nil), c.Files...)
	return c
}

// Started returns the ids of the commands still running (neither Complete nor Fail yet).
func (b *base) Started() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	var ids []int
	for _, c := range b.cmds {
		if c.Status == "started" {
			ids = append(ids, c.ID)
		}
	}
	return ids
}

// finish ends command id and returns the rows it imported (nil for an unknown id).
func (b *base) finish(id int, status, result, message string) []arr.Row {
	if id < 1 || id > len(b.cmds) {
		return nil
	}
	c := &b.cmds[id-1]
	c.Status, c.Result, c.Message = status, result, message
	return b.imported[id]
}

// moved calls OnImport for r.
func (b *base) moved(r arr.Row) {
	if b.OnImport != nil {
		b.OnImport(r)
	}
}

// FakeMovies is an in-memory arr.MovieManager.
type FakeMovies struct {
	base
	Library []arr.Movie // films in the library, by ID
	Results []arr.Movie // what Lookup returns, whatever the term
	// Queue holds the ids of films with an active download.
	Queue map[int]bool
	// Added records every Add call.
	Added []arr.AddMovie
	// Parsed maps a name to what Parse reads in it; any other name reads as Unknown.
	Parsed map[string]arr.Parsed
	// ParseCalls records the name of every Parse call.
	ParseCalls []string
	nextID     int
}

// Parse returns Parsed[title], or Unknown (the zero Parsed, revision 1).
func (f *FakeMovies) Parse(_ context.Context, title string) (arr.Parsed, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ParseCalls = append(f.ParseCalls, title)
	if err := f.fail("Parse"); err != nil {
		return arr.Parsed{}, err
	}
	if p, ok := f.Parsed[title]; ok {
		return p, nil
	}
	return arr.Parsed{Quality: arr.Quality{Name: "Unknown"}, Source: "unknown", Revision: 1}, nil
}

// Lookup returns Results, whatever the term. A result whose film is in Library (by TMDBID)
// carries the library's id, as a real lookup does.
func (f *FakeMovies) Lookup(context.Context, string) ([]arr.Movie, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]arr.Movie(nil), f.Results...)
	for i := range out {
		for _, m := range f.Library {
			if m.TMDBID == out[i].TMDBID && m.ID != 0 {
				out[i].ID = m.ID
			}
		}
	}
	return out, f.fail("Lookup")
}

func (f *FakeMovies) find(id int) int {
	for i, m := range f.Library {
		if m.ID == id {
			return i
		}
	}
	return -1
}

func (f *FakeMovies) Movie(_ context.Context, id int) (arr.Movie, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Movie"); err != nil {
		return arr.Movie{}, err
	}
	i := f.find(id)
	if i < 0 {
		return arr.Movie{}, fmt.Errorf("movie %d: %w", id, ErrNotFound)
	}
	return f.Library[i], nil
}

// Add puts a film with the next free id into the library, in the first root folder given.
func (f *FakeMovies) Add(_ context.Context, m arr.AddMovie) (arr.Movie, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Add"); err != nil {
		return arr.Movie{}, err
	}
	f.Added = append(f.Added, m)
	if f.nextID == 0 {
		f.nextID = 1000
	}
	f.nextID++
	mv := arr.Movie{ID: f.nextID, TMDBID: m.TMDBID, Monitored: m.Monitored}
	for _, r := range f.Results {
		if r.TMDBID == m.TMDBID {
			mv.Title, mv.Year, mv.OriginalLanguage = r.Title, r.Year, r.OriginalLanguage
		}
	}
	mv.Path = path.Join(m.RootFolder, fmt.Sprintf("%s (%d)", mv.Title, mv.Year))
	f.Library = append(f.Library, mv)
	return mv, nil
}

func (f *FakeMovies) Busy(_ context.Context, movieID int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Queue[movieID], f.fail("Busy")
}

func (f *FakeMovies) SetMonitored(_ context.Context, movieID int, monitored bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SetMonitored"); err != nil {
		return err
	}
	i := f.find(movieID)
	if i < 0 {
		return fmt.Errorf("movie %d: %w", movieID, ErrNotFound)
	}
	f.Library[i].Monitored = monitored
	return nil
}

// Complete finishes an import command successfully: each row's film gets the row's file
// as its tracked file.
func (f *FakeMovies) Complete(id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.finish(id, "completed", "successful", "") {
		f.give(id, r)
	}
}

// give makes r's file the tracked file of r's film.
func (f *FakeMovies) give(id int, r arr.Row) {
	if i := f.find(r.MovieID); i >= 0 {
		f.Library[i].File = &arr.MovieFile{
			ID: 5000 + id, Path: path.Join(f.Library[i].Path, path.Base(r.Path)),
			Size: r.Size, Quality: r.Quality, Languages: r.Languages,
		}
		f.Library[i].HasFile = true
		f.moved(r)
	}
}

// Fail finishes an import command as failed, with the app's message.
func (f *FakeMovies) Fail(id int, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finish(id, "failed", "unsuccessful", message)
}

// FailPartway imports the first n rows of an import command, then fails it with message.
func (f *FakeMovies) FailPartway(id, n int, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.finish(id, "failed", "unsuccessful", message)
	for _, r := range rows[:min(n, len(rows))] {
		f.give(id, r)
	}
}

// FakeSeries is an in-memory arr.SeriesManager.
type FakeSeries struct {
	base
	Library []arr.Series
	Results []arr.Series // what Lookup returns, whatever the term
	Eps     map[int][]arr.Episode
	Files   map[int][]arr.EpisodeFile // by series id: what EpisodeFiles returns
	Queue   map[int]bool
	Added   []arr.AddSeries
	// SeasonFlags records the season flags set through SetSeasonMonitored: series id →
	// season → monitored. Only the seasons a call named are present.
	SeasonFlags map[int]map[int]bool
	nextID      int
	nextFile    int
}

// Lookup returns Results, whatever the term. A result whose show is in Library (by TVDBID)
// carries the library's id, as a real lookup does.
func (f *FakeSeries) Lookup(context.Context, string) ([]arr.Series, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]arr.Series(nil), f.Results...)
	for i := range out {
		for _, s := range f.Library {
			if s.TVDBID == out[i].TVDBID && s.ID != 0 {
				out[i].ID = s.ID
			}
		}
	}
	return out, f.fail("Lookup")
}

func (f *FakeSeries) Series(_ context.Context, id int) (arr.Series, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Series"); err != nil {
		return arr.Series{}, err
	}
	for _, s := range f.Library {
		if s.ID == id {
			return s, nil
		}
	}
	return arr.Series{}, fmt.Errorf("series %d: %w", id, ErrNotFound)
}

func (f *FakeSeries) Add(_ context.Context, s arr.AddSeries) (arr.Series, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("Add"); err != nil {
		return arr.Series{}, err
	}
	f.Added = append(f.Added, s)
	if f.nextID == 0 {
		f.nextID = 2000
	}
	f.nextID++
	sr := arr.Series{ID: f.nextID, TVDBID: s.TVDBID, Type: s.Type}
	for _, r := range f.Results {
		if r.TVDBID == s.TVDBID {
			sr.Title, sr.Year, sr.OriginalLanguage, sr.Seasons = r.Title, r.Year, r.OriginalLanguage, r.Seasons
		}
	}
	sr.Path = path.Join(s.RootFolder, sr.Title)
	f.Library = append(f.Library, sr)
	return sr, nil
}

func (f *FakeSeries) Episodes(_ context.Context, seriesID int) ([]arr.Episode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]arr.Episode(nil), f.Eps[seriesID]...), f.fail("Episodes")
}

func (f *FakeSeries) EpisodeFiles(_ context.Context, seriesID int) ([]arr.EpisodeFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]arr.EpisodeFile(nil), f.Files[seriesID]...), f.fail("EpisodeFiles")
}

func (f *FakeSeries) Busy(_ context.Context, seriesID int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Queue[seriesID], f.fail("Busy")
}

func (f *FakeSeries) SetEpisodesMonitored(_ context.Context, ids []int, monitored bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SetEpisodesMonitored"); err != nil {
		return err
	}
	for _, id := range ids {
		for sid, eps := range f.Eps {
			for i := range eps {
				if eps[i].ID == id {
					f.Eps[sid][i].Monitored = monitored
				}
			}
		}
	}
	return nil
}

// SetSeasonMonitored records the season's flag and, as the app does, sets it on the
// season's episodes. No other season changes.
func (f *FakeSeries) SetSeasonMonitored(_ context.Context, seriesID, season int, monitored bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail("SetSeasonMonitored"); err != nil {
		return err
	}
	if f.SeasonFlags == nil {
		f.SeasonFlags = map[int]map[int]bool{}
	}
	if f.SeasonFlags[seriesID] == nil {
		f.SeasonFlags[seriesID] = map[int]bool{}
	}
	f.SeasonFlags[seriesID][season] = monitored
	for i := range f.Eps[seriesID] {
		if f.Eps[seriesID][i].SeasonNumber == season {
			f.Eps[seriesID][i].Monitored = monitored
		}
	}
	return nil
}

// Complete finishes an import command successfully: the episodes of each row get a file.
func (f *FakeSeries) Complete(id int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.finish(id, "completed", "successful", "") {
		f.give(r)
	}
}

// give makes r's file the file of each of r's episodes.
// The file gets an EpisodeFile record (in Files) with the row's size, as Sonarr's would.
func (f *FakeSeries) give(r arr.Row) {
	f.nextFile++
	id := 9000 + f.nextFile
	for _, ep := range r.EpisodeIDs {
		for sid, eps := range f.Eps {
			for i := range eps {
				if eps[i].ID == ep {
					f.Eps[sid][i].FileID = id
					if f.Files == nil {
						f.Files = map[int][]arr.EpisodeFile{}
					}
					if !slices.ContainsFunc(f.Files[sid], func(x arr.EpisodeFile) bool { return x.ID == id }) {
						f.Files[sid] = append(f.Files[sid], arr.EpisodeFile{ID: id, Path: path.Join("/tv", path.Base(r.Path)), Size: r.Size})
					}
				}
			}
		}
	}
	f.moved(r)
}

// Fail finishes an import command as failed, with the app's message.
func (f *FakeSeries) Fail(id int, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finish(id, "failed", "unsuccessful", message)
}

// FailPartway imports the first n rows of an import command, then fails it with message.
func (f *FakeSeries) FailPartway(id, n int, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows := f.finish(id, "failed", "unsuccessful", message)
	for _, r := range rows[:min(n, len(rows))] {
		f.give(r)
	}
}

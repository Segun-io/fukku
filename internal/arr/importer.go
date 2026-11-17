package arr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// The Importer methods are the same for both apps, so they live on the shared client.

func (c *client) Status(ctx context.Context) (Status, error) {
	var out struct {
		AppName string `json:"appName"`
		Version string `json:"version"`
	}
	if err := c.get(ctx, "/system/status", nil, &out); err != nil {
		return Status{}, err
	}
	return Status{AppName: out.AppName, Version: out.Version}, nil
}

func (c *client) Profiles(ctx context.Context) ([]Profile, error) {
	var out []struct {
		ID             int    `json:"id"`
		Name           string `json:"name"`
		MinFormatScore int    `json:"minFormatScore"`
	}
	if err := c.get(ctx, "/qualityprofile", nil, &out); err != nil {
		return nil, err
	}
	ps := make([]Profile, 0, len(out))
	for _, p := range out {
		ps = append(ps, Profile{ID: p.ID, Name: p.Name, MinFormatScore: p.MinFormatScore})
	}
	return ps, nil
}

func (c *client) RootFolders(ctx context.Context) ([]RootFolder, error) {
	var out []struct {
		Path      string `json:"path"`
		FreeSpace int64  `json:"freeSpace"`
	}
	if err := c.get(ctx, "/rootfolder", nil, &out); err != nil {
		return nil, err
	}
	fs := make([]RootFolder, 0, len(out))
	for _, f := range out {
		fs = append(fs, RootFolder{Path: f.Path, FreeSpace: f.FreeSpace})
	}
	return fs, nil
}

func (c *client) Qualities(ctx context.Context) ([]Quality, error) {
	var out []qualityJSON
	if err := c.get(ctx, "/qualitydefinition", nil, &out); err != nil {
		return nil, err
	}
	qs := make([]Quality, 0, len(out))
	for _, q := range out {
		qs = append(qs, Quality{ID: q.Quality.ID, Name: q.Quality.Name})
	}
	return qs, nil
}

func (c *client) Languages(ctx context.Context) ([]Language, error) {
	var out []languageJSON
	if err := c.get(ctx, "/language", nil, &out); err != nil {
		return nil, err
	}
	ls := make([]Language, 0, len(out))
	for _, l := range out {
		ls = append(ls, Language(l))
	}
	return ls, nil
}

// Proposal never sends a title id: with one, both apps ignore the folder (probes C6).
func (c *client) Proposal(ctx context.Context, folder string) ([]Row, error) {
	q := url.Values{"folder": {folder}, "filterExistingFiles": {"false"}}
	var items []json.RawMessage
	if err := c.get(ctx, "/manualimport", q, &items); err != nil {
		return nil, err
	}
	return decodeRows(items)
}

// UntrackedProposer is implemented by a client that can list only the files of a folder that
// the app does not track (filterExistingFiles=true): the re-check of episodes' versions (#96)
// asks it of a library's season folder, where the episodes' own files need no verdict.
type UntrackedProposer interface {
	UntrackedProposal(ctx context.Context, folder string) ([]Row, error)
}

var _ UntrackedProposer = (*client)(nil)

// UntrackedProposal is Proposal without the files the app already tracks.
func (c *client) UntrackedProposal(ctx context.Context, folder string) ([]Row, error) {
	q := url.Values{"folder": {folder}, "filterExistingFiles": {"true"}}
	var items []json.RawMessage
	if err := c.get(ctx, "/manualimport", q, &items); err != nil {
		return nil, err
	}
	return decodeRows(items)
}

func (c *client) Recheck(ctx context.Context, rows []Row) ([]Row, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	edited, err := encodeRows(rows)
	if err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := c.post(ctx, "/manualimport", edited, &items); err != nil {
		return nil, err
	}
	return decodeRows(items)
}

func (c *client) Import(ctx context.Context, rows []Row, mode string) (int, error) {
	if len(rows) == 0 {
		return 0, fmt.Errorf("%s: import: no files", c.app)
	}
	files, err := encodeRows(rows)
	if err != nil {
		return 0, err
	}
	body := map[string]any{"name": "ManualImport", "importMode": mode, "files": files}
	var out struct {
		ID int `json:"id"`
	}
	if err := c.post(ctx, "/command", body, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

type commandJSON struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Result  string `json:"result"`
	Message string `json:"message"`
	Body    *struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	} `json:"body"`
}

func (j commandJSON) command() Command {
	c := Command{ID: j.ID, Name: j.Name, Status: j.Status, Result: j.Result, Message: j.Message}
	if j.Body != nil {
		for _, f := range j.Body.Files {
			c.Files = append(c.Files, f.Path)
		}
	}
	return c
}

func (c *client) Command(ctx context.Context, id int) (Command, error) {
	var out commandJSON
	if err := c.get(ctx, fmt.Sprintf("/command/%d", id), nil, &out); err != nil {
		return Command{}, err
	}
	return out.command(), nil
}

func (c *client) Commands(ctx context.Context) ([]Command, error) {
	var out []commandJSON
	if err := c.get(ctx, "/command", nil, &out); err != nil {
		return nil, err
	}
	cs := make([]Command, 0, len(out))
	for _, j := range out {
		cs = append(cs, j.command())
	}
	return cs, nil
}

// ensureTag returns the id of the tag with the label, creating it if missing. The apps
// keep labels in lower case.
func (c *client) ensureTag(ctx context.Context, label string) (int, error) {
	label = strings.ToLower(label)
	var tags []struct {
		ID    int    `json:"id"`
		Label string `json:"label"`
	}
	if err := c.get(ctx, "/tag", nil, &tags); err != nil {
		return 0, err
	}
	for _, t := range tags {
		if strings.EqualFold(t.Label, label) {
			return t.ID, nil
		}
	}
	var created struct {
		ID int `json:"id"`
	}
	if err := c.post(ctx, "/tag", map[string]string{"label": label}, &created); err != nil {
		return 0, err
	}
	return created.ID, nil
}

// ---- rows ------------------------------------------------------------------------------

type qualityJSON struct {
	Quality struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"quality"`
}

type languageJSON struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type idJSON struct {
	ID int `json:"id"`
}

type rowJSON struct {
	Path          string         `json:"path"`
	Size          int64          `json:"size"`
	MovieID       int            `json:"movieId"`
	Movie         *idJSON        `json:"movie"`
	SeriesID      int            `json:"seriesId"`
	Series        *idJSON        `json:"series"`
	SeasonNumber  int            `json:"seasonNumber"`
	Episodes      []idJSON       `json:"episodes"`
	EpisodeIDs    []int          `json:"episodeIds"`
	Quality       qualityJSON    `json:"quality"`
	Languages     []languageJSON `json:"languages"`
	ReleaseGroup  string         `json:"releaseGroup"`
	CustomFormats []struct {
		Name string `json:"name"`
	} `json:"customFormats"`
	CustomFormatScore int `json:"customFormatScore"`
	Rejections        []struct {
		Reason string `json:"reason"`
		Type   string `json:"type"`
	} `json:"rejections"`
}

func decodeRows(items []json.RawMessage) ([]Row, error) {
	rows := make([]Row, 0, len(items))
	for _, raw := range items {
		var j rowJSON
		if err := json.Unmarshal(raw, &j); err != nil {
			return nil, fmt.Errorf("decode manual import item: %w", err)
		}
		r := Row{
			Path: j.Path, Size: j.Size, MovieID: j.MovieID, SeriesID: j.SeriesID,
			SeasonNumber: j.SeasonNumber, EpisodeIDs: j.EpisodeIDs,
			Quality:      Quality{ID: j.Quality.Quality.ID, Name: j.Quality.Quality.Name},
			ReleaseGroup: j.ReleaseGroup, CustomFormatScore: j.CustomFormatScore,
			Raw: append(json.RawMessage(nil), raw...),
		}
		if r.MovieID == 0 && j.Movie != nil {
			r.MovieID = j.Movie.ID
		}
		if r.SeriesID == 0 && j.Series != nil {
			r.SeriesID = j.Series.ID
		}
		if len(r.EpisodeIDs) == 0 {
			for _, e := range j.Episodes {
				r.EpisodeIDs = append(r.EpisodeIDs, e.ID)
			}
		}
		for _, l := range j.Languages {
			r.Languages = append(r.Languages, Language(l))
		}
		for _, f := range j.CustomFormats {
			r.CustomFormats = append(r.CustomFormats, f.Name)
		}
		for _, x := range j.Rejections {
			r.Rejections = append(r.Rejections, Rejection{Reason: x.Reason, Permanent: x.Type == "permanent"})
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func encodeRows(rows []Row) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(rows))
	for _, r := range rows {
		e, err := encodeRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// encodeRow writes the modelled fields of r over r.Raw and returns the result. Fields the
// row does not model, and the app's verdicts (rejections, custom formats), are left as the
// app sent them.
func encodeRow(r Row) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if len(r.Raw) > 0 {
		if err := json.Unmarshal(r.Raw, &obj); err != nil {
			return nil, fmt.Errorf("row %q: raw item: %w", r.Path, err)
		}
	}
	set := func(key string, v any) error {
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		obj[key] = data
		return nil
	}
	steps := []func() error{
		func() error { return set("path", r.Path) },
		func() error { return set("releaseGroup", r.ReleaseGroup) },
	}
	if r.Size > 0 {
		steps = append(steps, func() error { return set("size", r.Size) })
	}
	if r.MovieID > 0 {
		steps = append(steps, func() error { return set("movieId", r.MovieID) })
	}
	if r.SeriesID > 0 {
		steps = append(steps, func() error { return set("seriesId", r.SeriesID) })
		steps = append(steps, func() error { return set("seasonNumber", r.SeasonNumber) })
	}
	if len(r.EpisodeIDs) > 0 {
		steps = append(steps, func() error { return set("episodeIds", r.EpisodeIDs) })
	}
	if len(r.Languages) > 0 {
		steps = append(steps, func() error {
			ls := make([]languageJSON, 0, len(r.Languages))
			for _, l := range r.Languages {
				ls = append(ls, languageJSON(l))
			}
			return set("languages", ls)
		})
	}
	if r.Quality.ID != 0 || r.Quality.Name != "" {
		steps = append(steps, func() error { return setQuality(obj, set, r.Quality) })
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return nil, fmt.Errorf("row %q: %w", r.Path, err)
		}
	}
	return json.Marshal(obj)
}

// setQuality replaces quality.quality's id and name, keeping the revision and the quality's
// other fields (source, resolution).
func setQuality(obj map[string]json.RawMessage, set func(string, any) error, q Quality) error {
	outer := map[string]json.RawMessage{}
	if raw, ok := obj["quality"]; ok {
		_ = json.Unmarshal(raw, &outer)
	}
	inner := map[string]json.RawMessage{}
	if raw, ok := outer["quality"]; ok {
		_ = json.Unmarshal(raw, &inner)
	}
	id, _ := json.Marshal(q.ID)
	name, _ := json.Marshal(q.Name)
	inner["id"], inner["name"] = id, name
	in, err := json.Marshal(inner)
	if err != nil {
		return err
	}
	outer["quality"] = in
	return set("quality", outer)
}

// Command seed prepares the local test environment (make env-up). It is idempotent.
//
//	seed bazarr-config <config.yaml>   set Bazarr's fixed API key and its Radarr/Sonarr connections
//	                                   (run while Bazarr is stopped)
//	seed apps                          configure Radarr, Sonarr and Jellyfin over their APIs
//
// The settings mirror the first adopter's (plan §4) where they matter to the probes:
// renaming on, importExtraFiles off, no recycle bin, upgrades allowed, a "not original
// language" custom format, Jellyfin real-time monitoring off and no internet metadata.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/segun-io/fukku/test/testenv"
)

type M = testenv.M

// Fixed fake keys, matching test/env/compose.yaml.
const (
	bazarrKey = "cccccccccccccccccccccccccccccccc"
	radarrKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sonarrKey = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// Naming used by the probes. The film format is Radarr's default style, which B1 says
// Jellyfin never groups; the probes change it where they need another.
const (
	MovieFormat   = "{Movie Title} ({Release Year}) {Quality Full}"
	EpisodeFormat = "{Series Title} - S{season:00}E{episode:00} - {Episode Title} {Quality Full}"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: seed bazarr-config <file> | seed apps")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var err error
	switch os.Args[1] {
	case "bazarr-config":
		if len(os.Args) != 3 {
			err = errors.New("usage: seed bazarr-config <file>")
			break
		}
		err = bazarrConfig(os.Args[2])
	case "apps":
		err = apps(ctx)
		if errors.Is(err, errNeedJellyfinKey) {
			fmt.Fprintln(os.Stderr, "seed: jellyfin needs its API key inserted (exit 3)")
			os.Exit(3)
		}
	case "jellyfin-key":
		if len(os.Args) != 3 {
			err = errors.New("usage: seed jellyfin-key <jellyfin.db>")
			break
		}
		err = jellyfinKey(ctx, os.Args[2])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

// ---- Bazarr ---------------------------------------------------------------------------

func bazarrConfig(path string) error {
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := yaml.Unmarshal(data, doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	root := doc.Content[0]
	set := func(section, key string, value any) {
		sec := child(root, section)
		var n yaml.Node
		_ = n.Encode(value)
		for i := 0; i+1 < len(sec.Content); i += 2 {
			if sec.Content[i].Value == key {
				sec.Content[i+1] = &n
				return
			}
		}
		sec.Content = append(sec.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &n)
	}
	set("auth", "apikey", bazarrKey)
	set("general", "use_radarr", true)
	set("general", "use_sonarr", true)
	for _, app := range []struct {
		name, key string
		port      int
	}{{"radarr", radarrKey, 7878}, {"sonarr", sonarrKey, 8989}} {
		set(app.name, "ip", app.name)
		set(app.name, "port", app.port)
		set(app.name, "apikey", app.key)
		set(app.name, "base_url", "/")
		set(app.name, "ssl", false)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func child(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	n := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, n)
	return n
}

// ---- Radarr, Sonarr, Jellyfin ---------------------------------------------------------

func apps(ctx context.Context) error {
	env, err := testenv.FromEnv()
	if err != nil {
		return err
	}
	versions := map[string]string{}
	for _, a := range []*testenv.Arr{env.Radarr, env.Sonarr} {
		var v string
		if err := testenv.WaitFor(ctx, 3*time.Minute, 2*time.Second, a.Name, func() (bool, error) {
			var err error
			v, err = a.Version(ctx)
			return err == nil, err
		}); err != nil {
			return err
		}
		versions[a.Name] = v
	}
	if err := seedArr(ctx, env.Radarr, "/data/media/movies", 0); err != nil {
		return fmt.Errorf("radarr: %w", err)
	}
	if err := seedArr(ctx, env.Sonarr, "/data/media/tv", -10000); err != nil {
		return fmt.Errorf("sonarr: %w", err)
	}

	var bz struct {
		Data struct {
			BazarrVersion string `json:"bazarr_version"`
		} `json:"data"`
	}
	if err := testenv.WaitFor(ctx, 3*time.Minute, 2*time.Second, "bazarr", func() (bool, error) {
		err := env.Bazarr.Do(ctx, "GET", "/api/system/status", nil, &bz)
		return err == nil, err
	}); err != nil {
		return err
	}
	versions["bazarr"] = bz.Data.BazarrVersion
	if err := seedBazarrLanguages(ctx, env.Bazarr); err != nil {
		return fmt.Errorf("bazarr languages: %w", err)
	}

	jv, err := seedJellyfin(ctx, env)
	if err != nil {
		return fmt.Errorf("jellyfin: %w", err)
	}
	versions["jellyfin"] = jv

	if err := os.MkdirAll(env.Results, 0o750); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(versions, "", "  ")
	if err := os.WriteFile(filepath.Join(env.Results, "versions.json"), data, 0o600); err != nil {
		return err
	}
	fmt.Printf("seeded: %s\n", data)
	return nil
}

func seedArr(ctx context.Context, a *testenv.Arr, root string, notOriginalScore int) error {
	// Root folder.
	var roots []M
	if err := a.Get(ctx, "/rootfolder", &roots); err != nil {
		return err
	}
	found := false
	for _, r := range roots {
		found = found || r["path"] == root
	}
	if !found {
		if err := a.Post(ctx, "/rootfolder", M{"path": root}, nil); err != nil {
			return err
		}
	}

	// Media management: first-adopter settings.
	var mm M
	if err := a.Get(ctx, "/config/mediamanagement", &mm); err != nil {
		return err
	}
	mm["importExtraFiles"] = false
	mm["recycleBin"] = ""
	mm["copyUsingHardlinks"] = true
	mm["skipFreeSpaceCheckWhenImporting"] = true
	mm["createEmptyMovieFolders"] = false
	mm["deleteEmptyFolders"] = false
	if err := a.Put(ctx, "/config/mediamanagement", mm, nil); err != nil {
		return err
	}

	// Naming.
	var naming M
	if err := a.Get(ctx, "/config/naming", &naming); err != nil {
		return err
	}
	if a.Name == "radarr" {
		naming["renameMovies"] = true
		naming["standardMovieFormat"] = MovieFormat
		naming["movieFolderFormat"] = "{Movie Title} ({Release Year})"
	} else {
		naming["renameEpisodes"] = true
		naming["standardEpisodeFormat"] = EpisodeFormat
		naming["seasonFolderFormat"] = "Season {season:00}"
	}
	if err := a.Put(ctx, "/config/naming", naming, nil); err != nil {
		return err
	}

	// Custom format "Not Original Language".
	cf, err := a.FindByName(ctx, "/customformat", "Not Original Language")
	if err != nil {
		return err
	}
	if cf == nil {
		cf = M{}
		if err := a.Post(ctx, "/customformat", M{
			"name": "Not Original Language", "includeCustomFormatWhenRenaming": false,
			"specifications": []M{{
				"name": "Original", "implementation": "LanguageSpecification", "negate": true, "required": false,
				"fields": []M{{"name": "value", "value": -2}, {"name": "exceptLanguage", "value": false}},
			}},
		}, &cf); err != nil {
			return err
		}
	}
	cfID := testenv.ID(cf)

	// Quality profile "Fukku Probe": every quality allowed, upgrades allowed, the cutoff at
	// the top so an upgrade is always possible.
	existing, err := a.FindByName(ctx, "/qualityprofile", "Fukku Probe")
	if err != nil {
		return err
	}
	var base M
	if err := a.Get(ctx, "/qualityprofile/1", &base); err != nil {
		return err
	}
	profile := base
	if existing != nil {
		profile = existing
	}
	profile["name"] = "Fukku Probe"
	profile["upgradeAllowed"] = true
	items, _ := profile["items"].([]any)
	cutoff := 0
	for _, it := range items {
		item, _ := it.(M)
		item["allowed"] = true
		if q, ok := item["quality"].(M); ok {
			cutoff = testenv.ID(q)
		} else if id := testenv.ID(item); id != 0 {
			cutoff = id // a group
		}
	}
	profile["cutoff"] = cutoff
	profile["minFormatScore"] = 0
	profile["cutoffFormatScore"] = 10000
	var formatItems []M
	if raw, ok := profile["formatItems"].([]any); ok {
		for _, f := range raw {
			if fm, ok := f.(M); ok && testenv.ID(M{"id": fm["format"]}) != cfID {
				formatItems = append(formatItems, fm)
			}
		}
	}
	profile["formatItems"] = append(formatItems, M{"format": cfID, "name": "Not Original Language", "score": notOriginalScore})
	if a.Name == "radarr" {
		profile["language"] = M{"id": -2, "name": "Original"}
	}
	if existing != nil {
		return a.Put(ctx, fmt.Sprintf("/qualityprofile/%d", testenv.ID(existing)), profile, nil)
	}
	delete(profile, "id")
	return a.Post(ctx, "/qualityprofile", profile, nil)
}

// seedBazarrLanguages enables English and Spanish and a default profile for new titles;
// without them Bazarr reports a health issue and its subtitle jobs fail.
func seedBazarrLanguages(ctx context.Context, b *testenv.Client) error {
	profiles := `[{"profileId":1,"name":"Default","items":[` +
		`{"id":1,"language":"en","audio_exclude":"False","audio_only_include":"False","hi":"False","forced":"False"},` +
		`{"id":2,"language":"es","audio_exclude":"False","audio_only_include":"False","hi":"False","forced":"False"}],` +
		`"cutoff":null,"mustContain":[],"mustNotContain":[],"originalFormat":false,"tag":null}]`
	form := url.Values{
		"languages-enabled":                      {"en", "es"},
		"languages-profiles":                     {profiles},
		"settings-general-movie_default_enabled": {"true"},
		"settings-general-movie_default_profile": {"1"},
		"settings-general-serie_default_enabled": {"true"},
		"settings-general-serie_default_profile": {"1"},
	}
	return b.DoRaw(ctx, "POST", "/api/system/settings", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), nil, nil)
}

var errNeedJellyfinKey = errors.New("jellyfin does not know the test API key yet")

// jellyfinKey inserts the fixed API key into a stopped Jellyfin's database.
func jellyfinKey(ctx context.Context, db string) error {
	key := os.Getenv("FUKKU_TEST_JELLYFIN_KEY")
	if _, err := hex.DecodeString(key); err != nil || len(key) != 32 || strings.ToLower(key) != key {
		return errors.New("FUKKU_TEST_JELLYFIN_KEY must be 32 hex characters")
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05.0000000")
	sql := fmt.Sprintf("INSERT OR IGNORE INTO ApiKeys (DateCreated, DateLastActivity, Name, AccessToken) VALUES ('%s', '%s', 'fukku-test', '%s');", now, now, key)
	if out, err := exec.CommandContext(ctx, "sqlite3", db, sql).CombinedOutput(); err != nil {
		return fmt.Errorf("insert API key: %w: %s", err, out)
	}
	return nil
}

func seedJellyfin(ctx context.Context, env *testenv.Env) (string, error) {
	j := env.Jellyfin
	anon := &testenv.Jellyfin{Client: testenv.NewAnonymous(j.Base)}
	var info struct {
		Version                string `json:"Version"`
		StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
	}
	// /System/Info/Public answers before start-up finishes (and then reports the wizard as
	// not done), so wait for /health first.
	if err := testenv.WaitFor(ctx, 3*time.Minute, 2*time.Second, "jellyfin", func() (bool, error) {
		if err := anon.Do(ctx, "GET", "/health", nil, nil); err != nil {
			return false, err
		}
		err := anon.Do(ctx, "GET", "/System/Info/Public", nil, &info)
		return err == nil, err
	}); err != nil {
		return "", err
	}

	if !info.StartupWizardCompleted {
		steps := []struct {
			path string
			body any
		}{
			{"/Startup/Configuration", M{"UICulture": "en-US", "MetadataCountryCode": "US", "PreferredMetadataLanguage": "en"}},
			{"/Startup/User", M{"Name": "probe", "Password": "probe-password-not-a-secret"}},
			{"/Startup/RemoteAccess", M{"EnableRemoteAccess": true, "EnableAutomaticPortMapping": false}},
			{"/Startup/Complete", nil},
		}
		// GET /Startup/User creates the first user before it can be renamed.
		if err := anon.Do(ctx, "GET", "/Startup/User", nil, nil); err != nil {
			return "", err
		}
		for _, s := range steps {
			if err := anon.Do(ctx, "POST", s.path, s.body, nil); err != nil {
				return "", err
			}
		}
	}

	// The fixed API key: Jellyfin has no setting for one and locks its database while it
	// runs, so up.sh stops it, runs `seed jellyfin-key`, starts it and runs `seed apps` again.
	if err := j.Do(ctx, "GET", "/System/Info", nil, nil); testenv.StatusOf(err) == 401 {
		return "", errNeedJellyfinKey
	} else if err != nil {
		return "", err
	}

	// Libraries: real-time monitoring off, no internet metadata, as in the first adopter.
	var folders []struct {
		Name      string   `json:"Name"`
		Locations []string `json:"Locations"`
	}
	if err := j.Do(ctx, "GET", "/Library/VirtualFolders", nil, &folders); err != nil {
		return "", err
	}
	have := map[string]bool{}
	for _, f := range folders {
		have[f.Name] = true
	}
	for _, lib := range []struct{ name, kind, itemType, path string }{
		{"Movies", "movies", "Movie", "/data/media/movies"},
		{"Shows", "tvshows", "Series", "/data/media/tv"},
	} {
		if have[lib.name] {
			continue
		}
		opts := M{"LibraryOptions": M{
			"EnableRealtimeMonitor": false, "EnablePhotos": false, "EnableInternetProviders": false,
			"SaveLocalMetadata": false, "MetadataSavers": []string{}, "DisabledLocalMetadataReaders": []string{},
			"EnableEmbeddedTitles": false, "EnableChapterImageExtraction": false, "ExtractChapterImagesDuringLibraryScan": false,
			"AutomaticRefreshIntervalDays": 0, "EnableTrickplayImageExtraction": false,
			"PathInfos": []M{{"Path": lib.path}},
			"TypeOptions": []M{
				{"Type": lib.itemType, "MetadataFetchers": []string{}, "ImageFetchers": []string{}},
				{"Type": "Season", "MetadataFetchers": []string{}, "ImageFetchers": []string{}},
				{"Type": "Episode", "MetadataFetchers": []string{}, "ImageFetchers": []string{}},
			},
		}}
		q := testenv.Q("name", lib.name, "collectionType", lib.kind, "refreshLibrary", "false")
		if err := j.Do(ctx, "POST", "/Library/VirtualFolders"+q, opts, nil); err != nil {
			return "", err
		}
	}
	return info.Version, nil
}

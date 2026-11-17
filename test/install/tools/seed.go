package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"time"
)

// seed prepares the test apps. It is test scaffolding: in a real installation Radarr, Sonarr
// and Jellyfin are already set up, with root folders, a quality profile and (for Jellyfin)
// libraries and an API key. It does the least that makes the three usable: the root folders
// of Radarr and Sonarr (the quality profile HD-1080p that the example configuration names
// ships with both), and Jellyfin's start-up wizard, an API key and a Movies and a Shows
// library. The Jellyfin key is written to a file, for the operator's step 3.
func seed(args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	radarrURL := fs.String("radarr", "http://radarr:7878", "Radarr")
	sonarrURL := fs.String("sonarr", "http://sonarr:8989", "Sonarr")
	jellyfinURL := fs.String("jellyfin", "http://jellyfin:8096", "Jellyfin")
	keyOut := fs.String("jellyfin-key-file", "/scaffold/jellyfin_api_key", "where to write Jellyfin's API key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	for _, a := range []struct {
		name, url, keyEnv, root string
	}{
		{"radarr", *radarrURL, "RADARR_API_KEY", "/data/media/movies"},
		{"sonarr", *sonarrURL, "SONARR_API_KEY", "/data/media/tv"},
	} {
		key := os.Getenv(a.keyEnv)
		if key == "" {
			return fmt.Errorf("%s is not set", a.keyEnv)
		}
		c := newAPI(a.url, map[string]string{"X-Api-Key": key})
		if err := seedArr(ctx, a.name, c, a.root); err != nil {
			return fmt.Errorf("%s: %w", a.name, err)
		}
		fmt.Printf("seed: %s has the root folder %s\n", a.name, a.root)
	}
	key, err := seedJellyfin(ctx, *jellyfinURL)
	if err != nil {
		return fmt.Errorf("jellyfin: %w", err)
	}
	if err := os.WriteFile(*keyOut, []byte(key), 0o644); err != nil {
		return err
	}
	fmt.Println("seed: jellyfin has an API key and its libraries")
	return nil
}

func seedArr(ctx context.Context, name string, c *api, root string) error {
	if err := waitFor(ctx, name+" to answer", 4*time.Minute, func() error {
		return c.do(ctx, "GET", "/api/v3/system/status", nil, nil)
	}); err != nil {
		return err
	}
	var roots []obj
	if err := c.do(ctx, "GET", "/api/v3/rootfolder", nil, &roots); err != nil {
		return err
	}
	if !slices.ContainsFunc(roots, func(r obj) bool { return r["path"] == root }) {
		if err := c.do(ctx, "POST", "/api/v3/rootfolder", obj{"path": root}, nil); err != nil {
			return err
		}
	}
	var profiles []obj
	if err := c.do(ctx, "GET", "/api/v3/qualityprofile", nil, &profiles); err != nil {
		return err
	}
	if !slices.ContainsFunc(profiles, func(p obj) bool { return p["name"] == "HD-1080p" }) {
		return errors.New("the app has no quality profile HD-1080p, which deploy/fukku.example.yaml names")
	}
	return nil
}

const jellyfinClient = `MediaBrowser Client="fukku-install-test", Device="seed", DeviceId="fukku-install-test", Version="1"`

// seedJellyfin runs the start-up wizard, signs in as the wizard's user, creates an API key
// (Dashboard, API Keys) and the two libraries, and returns the key.
func seedJellyfin(ctx context.Context, base string) (string, error) {
	anon := newAPI(base, map[string]string{"Authorization": jellyfinClient})
	var info struct{ StartupWizardCompleted bool }
	if err := waitFor(ctx, "jellyfin to start", 5*time.Minute, func() error {
		if err := anon.do(ctx, "GET", "/health", nil, nil); err != nil {
			return err
		}
		return anon.do(ctx, "GET", "/System/Info/Public", nil, &info)
	}); err != nil {
		return "", err
	}
	const user, password = "install-test", "install-test-password-not-a-secret"
	if !info.StartupWizardCompleted {
		// GET /Startup/User creates the first user before it can be renamed.
		if err := anon.do(ctx, "GET", "/Startup/User", nil, nil); err != nil {
			return "", err
		}
		for _, s := range []struct {
			path string
			body any
		}{
			{"/Startup/Configuration", obj{"UICulture": "en-US", "MetadataCountryCode": "US", "PreferredMetadataLanguage": "en"}},
			{"/Startup/User", obj{"Name": user, "Password": password}},
			{"/Startup/RemoteAccess", obj{"EnableRemoteAccess": true, "EnableAutomaticPortMapping": false}},
			{"/Startup/Complete", nil},
		} {
			if err := anon.do(ctx, "POST", s.path, s.body, nil); err != nil {
				return "", err
			}
		}
	}
	var auth struct{ AccessToken string }
	if err := anon.do(ctx, "POST", "/Users/AuthenticateByName", obj{"Username": user, "Pw": password}, &auth); err != nil {
		return "", fmt.Errorf("sign in: %w", err)
	}
	j := newAPI(base, map[string]string{"Authorization": jellyfinClient + `, Token="` + auth.AccessToken + `"`})

	list := func() ([]string, error) {
		var keys struct {
			Items []struct{ AccessToken, AppName string }
		}
		if err := j.do(ctx, "GET", "/Auth/Keys", nil, &keys); err != nil {
			return nil, err
		}
		var out []string
		for _, k := range keys.Items {
			if k.AppName == "fukku-install-test" {
				out = append(out, k.AccessToken)
			}
		}
		return out, nil
	}
	keys, err := list()
	if err != nil {
		return "", err
	}
	if len(keys) == 0 {
		if err := j.do(ctx, "POST", "/Auth/Keys?app=fukku-install-test", nil, nil); err != nil {
			return "", err
		}
		if keys, err = list(); err != nil {
			return "", err
		}
	}
	if len(keys) == 0 {
		return "", errors.New("jellyfin made no API key")
	}

	var folders []struct{ Name string }
	if err := j.do(ctx, "GET", "/Library/VirtualFolders", nil, &folders); err != nil {
		return "", err
	}
	for _, lib := range []struct{ name, kind, itemType, path string }{
		{"Movies", "movies", "Movie", "/data/media/movies"},
		{"Shows", "tvshows", "Series", "/data/media/tv"},
	} {
		if slices.ContainsFunc(folders, func(f struct{ Name string }) bool { return f.Name == lib.name }) {
			continue
		}
		// No internet metadata: only Radarr's and Sonarr's metadata services may be reached.
		opts := obj{"LibraryOptions": obj{
			"EnableRealtimeMonitor": false, "EnableInternetProviders": false, "SaveLocalMetadata": false,
			"MetadataSavers": []string{}, "DisabledLocalMetadataReaders": []string{},
			"PathInfos": []obj{{"Path": lib.path}},
			"TypeOptions": []obj{
				{"Type": lib.itemType, "MetadataFetchers": []string{}, "ImageFetchers": []string{}},
				{"Type": "Season", "MetadataFetchers": []string{}, "ImageFetchers": []string{}},
				{"Type": "Episode", "MetadataFetchers": []string{}, "ImageFetchers": []string{}},
			},
		}}
		q := "?name=" + lib.name + "&collectionType=" + lib.kind + "&refreshLibrary=false"
		if err := j.do(ctx, "POST", "/Library/VirtualFolders"+q, opts, nil); err != nil {
			return "", fmt.Errorf("library %s: %w", lib.name, err)
		}
	}
	if err := primeLibraries(ctx, j); err != nil {
		return "", fmt.Errorf("prime the libraries: %w", err)
	}
	return keys[0], nil
}

// primeLibraries scans each library once while it holds a file, then removes the file. A real
// Jellyfin has scanned its libraries with their content long ago; a new one that never held a
// file makes the first import wait for mediaServer.itemWait (two minutes) before Fukku's
// fallback scan finds the title (docs/install.md, step 9). The placeholders are empty files
// with a video extension, and Jellyfin lists them as items by their names.
func primeLibraries(ctx context.Context, j *api) error {
	const name = "Seed Placeholder"
	placeholders := []struct{ dir, file, itemType string }{
		{"/data/media/movies/" + name + " (2000)", name + " (2000).mkv", "Movie"},
		{"/data/media/tv/" + name + "/Season 01", name + " - S01E01.mkv", "Series"},
	}
	for _, p := range placeholders {
		if err := os.MkdirAll(p.dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p.dir+"/"+p.file, nil, 0o644); err != nil {
			return err
		}
	}
	if err := j.do(ctx, "POST", "/Library/Refresh", nil, nil); err != nil {
		return err
	}
	var ids []string
	for _, p := range placeholders {
		var found struct{ Items []struct{ ID string } }
		if err := waitFor(ctx, "the placeholder "+p.itemType+" to be listed", 3*time.Minute, func() error {
			if err := j.do(ctx, "GET", "/Items?Recursive=true&IncludeItemTypes="+p.itemType+"&SearchTerm=Seed", nil, &found); err != nil {
				return err
			}
			if len(found.Items) == 0 {
				return errors.New("not listed yet")
			}
			return nil
		}); err != nil {
			return err
		}
		for _, it := range found.Items {
			ids = append(ids, it.ID)
		}
	}
	// The files go first (Jellyfin, as another user, could not remove them); an item whose
	// file is gone stays until it is removed, and a scan keeps it while the folder is empty.
	for _, d := range []string{"/data/media/movies/" + name + " (2000)", "/data/media/tv/" + name} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	for _, id := range ids {
		if err := j.do(ctx, "DELETE", "/Items/"+id, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

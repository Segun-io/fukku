package config

import (
	"strings"
	"testing"
)

func validateWith(t *testing.T, role Role, opts Options) error {
	t.Helper()
	return Validate(mustLoad(t, opts), role, ValidateOptions{})
}

func wantProblem(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("want an error mentioning %q, got %v", want, err)
	}
}

// #125: the worker holds no credentials, however they arrive.
func TestWorkerRefusesCredentials(t *testing.T) {
	t.Run("a secret setting", func(t *testing.T) {
		err := validateWith(t, RoleWorker, Options{Environ: []string{"FUKKU_SONARR_API_KEY=k"}})
		wantProblem(t, err, "sonarr.apiKey")
	})
	t.Run("a secret file, even an empty one", func(t *testing.T) {
		err := validateWith(t, RoleWorker, Options{
			Environ:  []string{"FUKKU_MEDIA_SERVER_API_KEY_FILE=/run/secrets/media"},
			ReadFile: files(map[string]string{"/run/secrets/media": "\n"}),
		})
		wantProblem(t, err, "mediaServer.apiKey")
		wantProblem(t, err, "FUKKU_MEDIA_SERVER_API_KEY_FILE")
	})
	t.Run("credentials in a URL", func(t *testing.T) {
		for _, env := range []string{
			"FUKKU_RADARR_URL=http://user:pw@radarr:7878",
			"FUKKU_SUBTITLES_URL=https://user@bazarr.example.com",
			"FUKKU_SERVER_PUBLIC_BASE_URL=https://user:pw@fukku.example.com",
		} {
			err := validateWith(t, RoleWorker, Options{Environ: []string{env}})
			wantProblem(t, err, "credentials in a URL")
		}
	})
	t.Run("a URL without user info and an @ elsewhere", func(t *testing.T) {
		err := validateWith(t, RoleWorker, Options{Environ: []string{"FUKKU_RADARR_URL=http://radarr:7878/path?x=a@b"}})
		if err != nil {
			t.Fatalf("refused: %v", err)
		}
	})
	t.Run("the web process may hold them", func(t *testing.T) {
		env := append([]string{"FUKKU_RADARR_API_KEY_FILE=/k"}, validWeb[:2]...)
		err := validateWith(t, RoleWeb, Options{Environ: env, ReadFile: files(map[string]string{"/k": "key"})})
		if err != nil {
			t.Fatal(err)
		}
	})
}

// #133: a /0 range, or any very wide one, is not a proxy address.
func TestTrustedProxiesTooWide(t *testing.T) {
	for _, tp := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/7", "2001:db8::/31", "128.0.0.0/1"} {
		env := []string{"FUKKU_AUTH_TRUSTED_PROXIES=" + tp, validWeb[1], validWeb[2]}
		wantProblem(t, validateWith(t, RoleWeb, Options{Environ: env}), "too wide")
		env = append(env, "FUKKU_AUTH_ALLOW_WIDE_TRUSTED_PROXIES=true")
		if err := validateWith(t, RoleWeb, Options{Environ: env}); err != nil {
			t.Errorf("%s with the override: %v", tp, err)
		}
	}
	for _, tp := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.0.2.10", "2001:db8::/32", "::1"} {
		env := []string{"FUKKU_AUTH_TRUSTED_PROXIES=" + tp, validWeb[1], validWeb[2]}
		if err := validateWith(t, RoleWeb, Options{Environ: env}); err != nil {
			t.Errorf("%s: %v", tp, err)
		}
	}
}

// #133: no groups header by default, and an admin group needs one.
func TestGroupsHeaderOffByDefault(t *testing.T) {
	if got := Defaults().Auth.GroupsHeader; got != "" {
		t.Fatalf("auth.groupsHeader defaults to %q", got)
	}
	env := append([]string{"FUKKU_AUTH_ADMIN_GROUP=media-admins"}, validWeb...)
	wantProblem(t, validateWith(t, RoleWeb, Options{Environ: env}), "auth.groupsHeader")
	env = append(env, "FUKKU_AUTH_GROUPS_HEADER=Remote-Groups")
	if err := validateWith(t, RoleWeb, Options{Environ: env}); err != nil {
		t.Fatal(err)
	}
}

// #130: the title link is an http(s) URL with the one placeholder.
func TestTitleLinkTemplateValidation(t *testing.T) {
	bad := []string{
		"javascript:alert({itemId})",
		"ftp://media.example.com/{itemId}",
		"//media.example.com/{itemId}",
		"https://media.example.com/web/",
		"https://media.example.com/{itemID}",
		"https://media.example.com/{itemId}/{other}",
		"https://user:pw@media.example.com/{itemId}",
	}
	for _, tmpl := range bad {
		env := append([]string{"FUKKU_MEDIA_SERVER_TITLE_LINK_TEMPLATE=" + tmpl}, validWeb...)
		wantProblem(t, validateWith(t, RoleWeb, Options{Environ: env}), "mediaServer.titleLinkTemplate")
	}
	for _, tmpl := range []string{
		"https://media.example.com/web/#/details?id={itemId}",
		"http://127.0.0.1:8096/web/index.html#!/details?id={itemId}&x=1",
		"https://media.example.com/items/{itemId}",
	} {
		env := append([]string{"FUKKU_MEDIA_SERVER_TITLE_LINK_TEMPLATE=" + tmpl}, validWeb...)
		if err := validateWith(t, RoleWeb, Options{Environ: env}); err != nil {
			t.Errorf("%s: %v", tmpl, err)
		}
	}
}

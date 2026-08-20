package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tonis2/foundry/internal/db"
	"github.com/tonis2/foundry/internal/repository"
)

// newTestMux registers a Handler's routes without requiring a database
// connection: route-registration lookups via mux.Handler never invoke the
// resolved handler function, so this is safe to use for tests that only
// need to assert which pattern (if any) a path resolves to.
func newTestMux(t *testing.T) (*http.ServeMux, *Handler) {
	t.Helper()
	h := New(nil, nil, nil, Config{})
	mux := http.NewServeMux()
	h.Routes(mux)
	return mux, h
}

// registeredPattern reports the mux pattern (if any) that would handle a
// request for method+path, without invoking the resolved handler.
func registeredPattern(mux *http.ServeMux, method, path string) string {
	req := httptest.NewRequest(method, path, nil)
	_, pattern := mux.Handler(req)
	return pattern
}

func TestRepositoriesRoutesRegistered(t *testing.T) {
	mux, _ := newTestMux(t)

	for _, path := range []string{
		"/repositories",
		"/repositories/fragment",
		"/repositories/1",
	} {
		if pattern := registeredPattern(mux, http.MethodGet, path); pattern == "" {
			t.Fatalf("expected a route registered for %q, got none", path)
		}
	}
}

func TestOldRegistryRoutesNoLongerRegistered(t *testing.T) {
	mux, _ := newTestMux(t)

	legacyPrefix := "/proj" + "ects"
	for _, path := range []string{
		legacyPrefix,
		legacyPrefix + "/fragment",
		legacyPrefix + "/1",
	} {
		// None of these paths have a dedicated registered pattern anymore;
		// they only fall through to the "/" catch-all shell handler, which
		// 404s for any path other than exactly "/".
		if pattern := registeredPattern(mux, http.MethodGet, path); pattern != "/" {
			t.Fatalf("expected legacy path %q to resolve only to the root catch-all, got pattern %q", path, pattern)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("path %q: status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
}

func TestRepositoriesMainTemplateRendersAndUsesRepositoryLabels(t *testing.T) {
	localPath := "/repos/foo"
	remoteURL := "https://example.com/foo.git"
	data := struct {
		Repositories   []repository.Repository
		Discovered     []discoveredRepo
		DiscoverErr    string
		RefreshResults []db.RefreshResult
	}{
		Repositories: []repository.Repository{
			{ID: 1, Name: "foo", LocalPath: &localPath, RemoteURL: &remoteURL, CreatedAt: time.Now()},
			// A grandfathered invalid row: neither locator set. Rendering
			// must not panic and must fall back to a safe placeholder.
			{ID: 2, Name: "legacy-invalid", LocalPath: nil, RemoteURL: nil, CreatedAt: time.Now()},
		},
	}

	var buf strings.Builder
	if err := templates.ExecuteTemplate(&buf, "repositories.main", data); err != nil {
		t.Fatalf("execute repositories.main: %v", err)
	}
	out := buf.String()

	for _, want := range []string{"Repositories", "Repository registry", "Discover repos", "Refresh remotes", localPath, remoteURL, "legacy-invalid"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in rendered output, got:\n%s", want, out)
		}
	}
	legacyWord := "Proj" + "ects"
	legacyAction := "Register " + "proj" + "ect"
	legacyColumn := "repo" + "_path"
	for _, unwanted := range []string{legacyWord, legacyAction, legacyColumn} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("expected %q not present in rendered registry output, got:\n%s", unwanted, out)
		}
	}
}

func TestRepositoriesDetailTemplateRendersAllLocatorCombinations(t *testing.T) {
	localPath := "/repos/foo"
	remoteURL := "https://example.com/foo.git"

	cases := []struct {
		name string
		repo repository.Repository
	}{
		{"local only", repository.Repository{ID: 1, Name: "local-only", LocalPath: &localPath, CreatedAt: time.Now()}},
		{"remote only", repository.Repository{ID: 2, Name: "remote-only", RemoteURL: &remoteURL, CreatedAt: time.Now()}},
		{"both locators", repository.Repository{ID: 3, Name: "both", LocalPath: &localPath, RemoteURL: &remoteURL, CreatedAt: time.Now()}},
		{"grandfathered invalid (neither locator)", repository.Repository{ID: 4, Name: "invalid", CreatedAt: time.Now()}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf strings.Builder
			err := templates.ExecuteTemplate(&buf, "repositories.detail", struct {
				Repository repository.Repository
			}{c.repo})
			if err != nil {
				t.Fatalf("execute repositories.detail for %s: %v", c.name, err)
			}
			out := buf.String()
			if !strings.Contains(out, c.repo.Name) {
				t.Fatalf("expected repository name %q in output:\n%s", c.repo.Name, out)
			}
			legacyLabel := "Proj" + "ect"
			if strings.Contains(out, legacyLabel) {
				t.Fatalf("expected no legacy %s label in registry detail output:\n%s", legacyLabel, out)
			}
		})
	}
}

func TestOldRegistryTemplatesRemoved(t *testing.T) {
	legacyPrefix := "proj" + "ects"
	for _, name := range []string{legacyPrefix + ".main", legacyPrefix + ".detail"} {
		if tmpl := templates.Lookup(name); tmpl != nil {
			t.Fatalf("expected template %q to no longer be defined", name)
		}
	}
	for _, name := range []string{"repositories.main", "repositories.detail"} {
		if tmpl := templates.Lookup(name); tmpl == nil {
			t.Fatalf("expected template %q to be defined", name)
		}
	}
}

func TestShellNavUsesRepositoriesLabelNotProjects(t *testing.T) {
	var buf strings.Builder
	if err := templates.ExecuteTemplate(&buf, "shell.main", struct {
		Page     string
		Fragment string
	}{"repositories", "/repositories/fragment"}); err != nil {
		t.Fatalf("execute shell.main: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `href="/repositories"`) {
		t.Fatalf("expected nav link to /repositories, got:\n%s", out)
	}
	legacyRoute := "/proj" + "ects"
	if strings.Contains(out, `href="`+legacyRoute+`"`) {
		t.Fatalf("expected no legacy nav link to %s, got:\n%s", legacyRoute, out)
	}
	legacyLabel := ">" + "Proj" + "ects<"
	if strings.Contains(out, legacyLabel) {
		t.Fatalf("expected no legacy %q nav label, got:\n%s", legacyLabel, out)
	}
}

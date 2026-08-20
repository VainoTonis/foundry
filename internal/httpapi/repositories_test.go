package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

// requireGit and initRepo mirror the fixtures in internal/db's and
// internal/repository's test suites: they set up a real, non-bare git
// worktree so that path-canonicalization validation in the production
// code path succeeds instead of being short-circuited by a fake path.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v: %s", err, out)
	}
}

// testDBURLEnv names the environment variable that opts this suite into
// running against a real PostgreSQL instance, mirroring
// internal/db's integration test gating.
const testDBURLEnv = "FOUNDRY_TEST_DATABASE_URL"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv(testDBURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL integration test", testDBURLEnv)
	}

	migrationsPath, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations path: %v", err)
	}

	m, err := migrate.New("file:///"+migrationsPath, url)
	if err != nil {
		t.Fatalf("migrate init: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
		t.Fatalf("migrate close: src=%v db=%v", srcErr, dbErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pool.Ping: %v", err)
	}
	return pool
}

func newRepositoriesHandler(t *testing.T) *Handler {
	t.Helper()
	pool := testPool(t)
	return New(pool, Config{})
}

func TestHandleRepositoriesCreateAndList(t *testing.T) {
	h := newRepositoriesHandler(t)

	body := strings.NewReader(`{"name":"foo","remote_url":"https://github.com/foo/bar.git"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/repositories", body)
	rec := httptest.NewRecorder()
	h.HandleRepositories(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID        int64   `json:"id"`
		Name      string  `json:"name"`
		LocalPath *string `json:"local_path"`
		RemoteURL *string `json:"remote_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if created.Name != "foo" || created.LocalPath != nil || created.RemoteURL == nil || *created.RemoteURL != "https://github.com/foo/bar.git" {
		t.Fatalf("unexpected created repository: %+v", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/repositories", nil)
	listRec := httptest.NewRecorder()
	h.HandleRepositories(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), `"foo"`) {
		t.Fatalf("list response missing created repository: %s", listRec.Body.String())
	}
}

func TestHandleRepositoriesCreateRejectsMissingLocator(t *testing.T) {
	h := newRepositoriesHandler(t)

	body := strings.NewReader(`{"name":"no-locator"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/repositories", body)
	rec := httptest.NewRecorder()
	h.HandleRepositories(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleRepositoryGetUpdateDelete(t *testing.T) {
	h := newRepositoriesHandler(t)

	createReq := httptest.NewRequest(http.MethodPost, "/api/repositories", strings.NewReader(
		`{"name":"bar","local_path":null,"remote_url":"https://github.com/foo/baz.git"}`))
	createRec := httptest.NewRecorder()
	h.HandleRepositories(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}

	getPath := "/api/repositories/" + itoa(created.ID)
	getReq := httptest.NewRequest(http.MethodGet, getPath, nil)
	getRec := httptest.NewRecorder()
	h.HandleRepository(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", getRec.Code, getRec.Body.String())
	}

	// Explicit null clears remote_url, but the update must still satisfy
	// the at-least-one-locator invariant, so set local_path is omitted
	// here and only the name is changed instead.
	patchReq := httptest.NewRequest(http.MethodPatch, getPath, strings.NewReader(`{"name":"bar-renamed"}`))
	patchRec := httptest.NewRecorder()
	h.HandleRepository(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status = %d body = %s", patchRec.Code, patchRec.Body.String())
	}
	var updated struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(patchRec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal patch response: %v", err)
	}
	if updated.Name != "bar-renamed" {
		t.Fatalf("updated.Name = %q, want bar-renamed", updated.Name)
	}

	// Explicit null on the only remaining locator must be rejected.
	badPatchReq := httptest.NewRequest(http.MethodPatch, getPath, strings.NewReader(`{"remote_url":null}`))
	badPatchRec := httptest.NewRecorder()
	h.HandleRepository(badPatchRec, badPatchReq)
	if badPatchRec.Code != http.StatusBadRequest {
		t.Fatalf("bad patch status = %d, want %d, body = %s", badPatchRec.Code, http.StatusBadRequest, badPatchRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, getPath, nil)
	deleteRec := httptest.NewRecorder()
	h.HandleRepository(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body = %s", deleteRec.Code, deleteRec.Body.String())
	}

	notFoundReq := httptest.NewRequest(http.MethodGet, getPath, nil)
	notFoundRec := httptest.NewRecorder()
	h.HandleRepository(notFoundRec, notFoundReq)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("get-after-delete status = %d, want %d", notFoundRec.Code, http.StatusNotFound)
	}
}

// TestHandleRepositoryPatchLocatorPresenceSemantics is a PostgreSQL-backed
// end-to-end check of the PATCH presence-aware decoding contract: a
// wholly omitted locator key leaves the corresponding DB column
// unchanged, an explicitly null locator key clears it (subject to the
// at-least-one-locator invariant), and attempting to clear the only
// remaining locator is rejected with 400 and mutates nothing.
func TestHandleRepositoryPatchLocatorPresenceSemantics(t *testing.T) {
	requireGit(t)
	h := newRepositoriesHandler(t)

	root := t.TempDir()
	initRepo(t, root)
	wantLocalPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", root, err)
	}

	createBody, err := json.Marshal(map[string]any{
		"name":       "presence",
		"local_path": root,
		"remote_url": "https://github.com/foo/presence.git",
	})
	if err != nil {
		t.Fatalf("marshal create body: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/repositories", strings.NewReader(string(createBody)))
	createRec := httptest.NewRecorder()
	h.HandleRepositories(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		ID        int64   `json:"id"`
		LocalPath *string `json:"local_path"`
		RemoteURL *string `json:"remote_url"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	if created.LocalPath == nil {
		t.Fatal("created.LocalPath = nil, want canonical git worktree top-level path")
	}
	if gotLocalPath, err := filepath.EvalSymlinks(*created.LocalPath); err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", *created.LocalPath, err)
	} else if gotLocalPath != wantLocalPath {
		t.Fatalf("created.LocalPath = %q, want %q", gotLocalPath, wantLocalPath)
	}
	getPath := "/api/repositories/" + itoa(created.ID)

	// Omitting remote_url from the PATCH body must leave it unchanged in
	// the database, even though local_path is being cleared in the same
	// request (remote_url remains as the sole surviving locator).
	omitReq := httptest.NewRequest(http.MethodPatch, getPath, strings.NewReader(`{"local_path":null}`))
	omitRec := httptest.NewRecorder()
	h.HandleRepository(omitRec, omitReq)
	if omitRec.Code != http.StatusOK {
		t.Fatalf("omit patch status = %d, want %d, body = %s", omitRec.Code, http.StatusOK, omitRec.Body.String())
	}
	var afterOmit struct {
		LocalPath *string `json:"local_path"`
		RemoteURL *string `json:"remote_url"`
	}
	if err := json.Unmarshal(omitRec.Body.Bytes(), &afterOmit); err != nil {
		t.Fatalf("unmarshal omit-patch response: %v", err)
	}
	if afterOmit.LocalPath != nil {
		t.Fatalf("afterOmit.LocalPath = %v, want nil (explicitly cleared)", afterOmit.LocalPath)
	}
	if afterOmit.RemoteURL == nil || *afterOmit.RemoteURL != *created.RemoteURL {
		t.Fatalf("afterOmit.RemoteURL = %v, want unchanged %v (omitted key)", afterOmit.RemoteURL, created.RemoteURL)
	}

	// Now only remote_url remains set. Explicitly nulling it is the only
	// remaining locator and must be rejected with 400, without mutating
	// the stored row.
	clearOnlyReq := httptest.NewRequest(http.MethodPatch, getPath, strings.NewReader(`{"remote_url":null}`))
	clearOnlyRec := httptest.NewRecorder()
	h.HandleRepository(clearOnlyRec, clearOnlyReq)
	if clearOnlyRec.Code != http.StatusBadRequest {
		t.Fatalf("clear-only-locator status = %d, want %d, body = %s", clearOnlyRec.Code, http.StatusBadRequest, clearOnlyRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, getPath, nil)
	getRec := httptest.NewRecorder()
	h.HandleRepository(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get-after-rejected-patch status = %d body = %s", getRec.Code, getRec.Body.String())
	}
	var afterRejected struct {
		LocalPath *string `json:"local_path"`
		RemoteURL *string `json:"remote_url"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &afterRejected); err != nil {
		t.Fatalf("unmarshal get-after-rejected-patch response: %v", err)
	}
	if afterRejected.LocalPath != nil {
		t.Fatalf("afterRejected.LocalPath = %v, want nil (unchanged from previous step)", afterRejected.LocalPath)
	}
	if afterRejected.RemoteURL == nil || *afterRejected.RemoteURL != *created.RemoteURL {
		t.Fatalf("afterRejected.RemoteURL = %v, want unchanged %v (rejected patch must not mutate)", afterRejected.RemoteURL, created.RemoteURL)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, getPath, nil)
	deleteRec := httptest.NewRecorder()
	h.HandleRepository(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body = %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestHandleRepositoryInvalidID(t *testing.T) {
	h := newRepositoriesHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/api/repositories/not-a-number", nil)
	rec := httptest.NewRecorder()
	h.HandleRepository(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

// The tests below exercise request-validation logic that returns before
// any database access, so they run against a Handler with a nil pool and
// do not require FOUNDRY_TEST_DATABASE_URL.

func TestHandleRepositoriesCreateRejectsMalformedJSON(t *testing.T) {
	h := New(nil, Config{})

	req := httptest.NewRequest(http.MethodPost, "/api/repositories", strings.NewReader(`{`))
	rec := httptest.NewRecorder()
	h.HandleRepositories(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleRepositoriesRejectsUnsupportedMethod(t *testing.T) {
	h := New(nil, Config{})

	req := httptest.NewRequest(http.MethodPut, "/api/repositories", nil)
	rec := httptest.NewRecorder()
	h.HandleRepositories(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}

func TestHandleRepositoryInvalidIDDoesNotTouchDatabase(t *testing.T) {
	h := New(nil, Config{})

	for _, path := range []string{
		"/api/repositories/not-a-number",
		"/api/repositories/",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.HandleRepository(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("path %q: status = %d, want %d, body = %s", path, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	}
}

func TestHandleRepositoryRejectsUnsupportedMethod(t *testing.T) {
	h := New(nil, Config{})

	req := httptest.NewRequest(http.MethodPut, "/api/repositories/1", nil)
	rec := httptest.NewRecorder()
	h.HandleRepository(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusMethodNotAllowed, rec.Body.String())
	}
}

func TestHandleRepositoryUpdateRejectsMalformedJSON(t *testing.T) {
	h := New(nil, Config{})

	req := httptest.NewRequest(http.MethodPatch, "/api/repositories/1", strings.NewReader(`{`))
	rec := httptest.NewRecorder()
	h.HandleRepository(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestHandleRepositoriesCreateRejectsInvalidLocalPath verifies that a
// malformed (non-absolute) local_path is classified as a client validation
// error (HTTP 400) rather than surfacing as a 500, and that this happens
// before any database access since it uses a nil pool.
func TestHandleRepositoriesCreateRejectsInvalidLocalPath(t *testing.T) {
	h := New(nil, Config{})

	body := strings.NewReader(`{"name":"bad-local","local_path":"relative/path"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/repositories", body)
	rec := httptest.NewRecorder()
	h.HandleRepositories(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestHandleRepositoriesCreateRejectsInvalidRemoteURL verifies that a
// malformed/unsupported remote_url is classified as a client validation
// error (HTTP 400) rather than a 500, without touching the database.
func TestHandleRepositoriesCreateRejectsInvalidRemoteURL(t *testing.T) {
	h := New(nil, Config{})

	cases := []string{
		`{"name":"bad-remote","remote_url":"ftp://example.com/foo/bar.git"}`,
		`{"name":"bad-remote","remote_url":"https:///foo/bar.git"}`,
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/api/repositories", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleRepositories(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want %d, body = %s", body, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	}
}

// TestHandleRepositoryUpdateRejectsInvalidLocalPath verifies that PATCH
// classifies a malformed local_path as a 400, before any database access.
func TestHandleRepositoryUpdateRejectsInvalidLocalPath(t *testing.T) {
	h := New(nil, Config{})

	req := httptest.NewRequest(http.MethodPatch, "/api/repositories/1", strings.NewReader(
		`{"local_path":"relative/path"}`))
	rec := httptest.NewRecorder()
	h.HandleRepository(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

// TestHandleRepositoryUpdateRejectsInvalidRemoteURL verifies that PATCH
// classifies a malformed/unsupported remote_url as a 400, before any
// database access.
func TestHandleRepositoryUpdateRejectsInvalidRemoteURL(t *testing.T) {
	h := New(nil, Config{})

	req := httptest.NewRequest(http.MethodPatch, "/api/repositories/1", strings.NewReader(
		`{"remote_url":"ftp://example.com/foo/bar.git"}`))
	rec := httptest.NewRecorder()
	h.HandleRepository(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleRepositoryUpdateRejectsNonStringLocator(t *testing.T) {
	h := New(nil, Config{})

	for _, body := range []string{
		`{"local_path":123}`,
		`{"remote_url":true}`,
	} {
		req := httptest.NewRequest(http.MethodPatch, "/api/repositories/1", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.HandleRepository(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want %d, body = %s", body, rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	}
}

// TestDecodeRepositoryUpdateBodyDistinguishesOmittedNullAndValue verifies
// the three-state locator semantics that HandleRepository's PATCH path
// relies on, decoding a full JSON request body rather than a bare
// json.RawMessage: a key wholly absent from the object must leave the
// locator unchanged (IsSet == false), a key explicitly present with a
// JSON null value must mark the locator for clearing (IsSet == true,
// Value == nil), and a key present with a string value must mark the
// locator for replacement (IsSet == true, Value == &s). This must be
// checked against the full decode path (not just the per-field helper)
// because encoding/json collapses an absent key and an explicit null into
// the same zero value when decoding directly into a *json.RawMessage
// struct field, which silently defeats the omitted/null distinction.
func TestDecodeRepositoryUpdateBodyDistinguishesOmittedNullAndValue(t *testing.T) {
	// Omitted: the key is absent from the JSON object entirely.
	omitted, err := decodeRepositoryUpdateBody([]byte(`{}`))
	if err != nil {
		t.Fatalf("omitted: unexpected error: %v", err)
	}
	if omitted.LocalPath.IsSet() {
		t.Fatalf("omitted: LocalPath.IsSet() = true, want false")
	}
	if omitted.RemoteURL.IsSet() {
		t.Fatalf("omitted: RemoteURL.IsSet() = true, want false")
	}

	// Explicit null: the key is present with a JSON null value.
	explicitNull, err := decodeRepositoryUpdateBody([]byte(`{"local_path":null}`))
	if err != nil {
		t.Fatalf("explicit null: unexpected error: %v", err)
	}
	if !explicitNull.LocalPath.IsSet() {
		t.Fatalf("explicit null: LocalPath.IsSet() = false, want true")
	}
	if explicitNull.LocalPath.Value() != nil {
		t.Fatalf("explicit null: LocalPath.Value() = %v, want nil", explicitNull.LocalPath.Value())
	}
	if explicitNull.RemoteURL.IsSet() {
		t.Fatalf("explicit null: RemoteURL.IsSet() = true, want false (omitted key)")
	}

	// Explicit value: the key is present with a JSON string value.
	explicitValue, err := decodeRepositoryUpdateBody([]byte(`{"remote_url":"https://example.com/repo.git"}`))
	if err != nil {
		t.Fatalf("explicit value: unexpected error: %v", err)
	}
	if !explicitValue.RemoteURL.IsSet() {
		t.Fatalf("explicit value: RemoteURL.IsSet() = false, want true")
	}
	v := explicitValue.RemoteURL.Value()
	if v == nil || *v != "https://example.com/repo.git" {
		t.Fatalf("explicit value: RemoteURL.Value() = %v, want https://example.com/repo.git", v)
	}

	// Name follows plain nil-means-unchanged pointer semantics, unlike the
	// locator fields, and is unaffected by this presence-aware decoding.
	named, err := decodeRepositoryUpdateBody([]byte(`{"name":"renamed"}`))
	if err != nil {
		t.Fatalf("name: unexpected error: %v", err)
	}
	if named.Name == nil || *named.Name != "renamed" {
		t.Fatalf("name: Name = %v, want renamed", named.Name)
	}
}

// TestDecodeRepositoryUpdateBodyRejectsNonStringNonNull verifies that a
// locator field present with a non-string, non-null JSON value (e.g. a
// number or boolean) is rejected rather than silently coerced, since the
// wire contract for local_path/remote_url is string-or-null.
func TestDecodeRepositoryUpdateBodyRejectsNonStringNonNull(t *testing.T) {
	for _, body := range []string{
		`{"local_path":123}`,
		`{"local_path":true}`,
		`{"local_path":{}}`,
		`{"local_path":[]}`,
		`{"remote_url":123}`,
		`{"remote_url":true}`,
		`{"remote_url":{}}`,
		`{"remote_url":[]}`,
	} {
		if _, err := decodeRepositoryUpdateBody([]byte(body)); err == nil {
			t.Fatalf("body %q: expected error, got nil", body)
		}
	}
}

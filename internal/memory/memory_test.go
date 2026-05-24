package memory

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadApprovedMapsNamespaceToMemoryRepoDirectory(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, "team", "project")
	mustWriteFile(t, filepath.Join(root, "overview.md"), " overview memory \n")
	mustWriteFile(t, filepath.Join(root, "nested", "notes.MD"), "nested memory\n")
	mustWriteFile(t, filepath.Join(root, "ignore.txt"), "not markdown")
	mustWriteFile(t, filepath.Join(root, ".hidden.md"), "hidden file")
	mustWriteFile(t, filepath.Join(root, ".secret", "notes.md"), "hidden dir")
	mustWriteFile(t, filepath.Join(repo, "other-project", "overview.md"), "wrong namespace")

	slice, err := LoadApproved(repo, " team/project ")
	if err != nil {
		t.Fatalf("LoadApproved returned error: %v", err)
	}

	if slice.RepoPath != repo {
		t.Fatalf("RepoPath = %q, want %q", slice.RepoPath, repo)
	}
	if slice.Namespace != "team/project" {
		t.Fatalf("Namespace = %q, want team/project", slice.Namespace)
	}
	if slice.Root != filepath.Join(repo, "team", "project") {
		t.Fatalf("Root = %q, want namespace directory under memory repo", slice.Root)
	}

	gotPaths := make([]string, 0, len(slice.Files))
	for _, f := range slice.Files {
		gotPaths = append(gotPaths, f.Path)
	}
	wantPaths := []string{"nested/notes.MD", "overview.md"}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("loaded paths = %#v, want %#v", gotPaths, wantPaths)
	}
	if slice.Files[1].Content != "overview memory" {
		t.Fatalf("content was not trimmed: %q", slice.Files[1].Content)
	}
	if !strings.Contains(slice.Markdown, "Memory namespace: team/project") || strings.Contains(slice.Markdown, "wrong namespace") || strings.Contains(slice.Markdown, "hidden") {
		t.Fatalf("formatted markdown did not stay within approved namespace/files:\n%s", slice.Markdown)
	}
}

func TestWriteApprovedUpdateStaysInsideNamespaceWorkflowUpdates(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	targetRepo := t.TempDir()

	path, err := WriteApprovedUpdate(repo, "project-a", 42, " update body \n")
	if err != nil {
		t.Fatalf("WriteApprovedUpdate returned error: %v", err)
	}

	wantPath := filepath.Join(repo, "project-a", "workflow-updates", "workflow-42.md")
	if path != wantPath {
		t.Fatalf("path = %q, want %q", path, wantPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written update: %v", err)
	}
	if string(data) != "update body\n" {
		t.Fatalf("written content = %q, want trimmed body with one newline", string(data))
	}
	if entries, err := os.ReadDir(targetRepo); err != nil || len(entries) != 0 {
		t.Fatalf("target repo should remain untouched; entries=%d err=%v", len(entries), err)
	}
	log := gitOutput(t, repo, "log", "--oneline", "--", filepath.ToSlash(filepath.Join("project-a", "workflow-updates", "workflow-42.md")))
	if !strings.Contains(log, "Accept memory update for workflow 42") {
		t.Fatalf("memory update was not committed; log:\n%s", log)
	}
}

func TestWriteApprovedUpdateReturnsClearCommitError(t *testing.T) {
	repo := t.TempDir()

	_, err := WriteApprovedUpdate(repo, "project-a", 42, "body")
	if err == nil || !strings.Contains(err.Error(), "git add memory update failed") {
		t.Fatalf("error = %v, want clear git add failure", err)
	}
}

func TestMemoryNamespaceTraversalIsRejected(t *testing.T) {
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, "other", "approved.md"), "other namespace")

	badNamespaces := []string{
		"../outside",
		"project/../other",
		"project/../../outside",
		"./project",
	}
	for _, namespace := range badNamespaces {
		t.Run(namespace, func(t *testing.T) {
			if _, err := LoadApproved(repo, namespace); err == nil {
				t.Fatalf("LoadApproved(%q) succeeded, want traversal/relative component error", namespace)
			}
			if _, err := WriteApprovedUpdate(repo, namespace, 7, "body"); err == nil {
				t.Fatalf("WriteApprovedUpdate(%q) succeeded, want traversal/relative component error", namespace)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(repo, "outside")); !os.IsNotExist(err) {
		t.Fatalf("outside directory was created or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "other", "workflow-updates")); !os.IsNotExist(err) {
		t.Fatalf("sibling namespace workflow-updates was created or stat failed unexpectedly: %v", err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func initGitRepo(t *testing.T, repo string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	gitOutput(t, repo, "init")
	gitOutput(t, repo, "config", "user.email", "test@example.com")
	gitOutput(t, repo, "config", "user.name", "Test User")
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmdArgs := append([]string{"-C", repo}, args...)
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

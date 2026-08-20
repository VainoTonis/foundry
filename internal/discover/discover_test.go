package discover

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, out)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "initial")
}

func resolvedPaths(t *testing.T, repos []Repo) []string {
	t.Helper()
	var out []string
	for _, r := range repos {
		resolved, err := filepath.EvalSymlinks(r.Path)
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", r.Path, err)
		}
		out = append(out, resolved)
	}
	sort.Strings(out)
	return out
}

func TestFindRepos(t *testing.T) {
	t.Run("finds a repo nested under root", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		repoDir := filepath.Join(root, "group", "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, repoDir)

		repos, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v", err)
		}
		want, err := filepath.EvalSymlinks(repoDir)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		got := resolvedPaths(t, repos)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("FindRepos() = %v, want [%q]", got, want)
		}
	})

	t.Run("finds a repository nested inside another repository's worktree", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		outer := filepath.Join(root, "outer")
		if err := os.MkdirAll(outer, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, outer)

		inner := filepath.Join(outer, "vendored", "inner")
		if err := os.MkdirAll(inner, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, inner)

		repos, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v", err)
		}

		wantOuter, err := filepath.EvalSymlinks(outer)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		wantInner, err := filepath.EvalSymlinks(inner)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		want := []string{wantInner, wantOuter}
		sort.Strings(want)

		got := resolvedPaths(t, repos)
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("FindRepos() = %v, want %v", got, want)
		}
	})

	t.Run("finds a linked worktree and deduplicates it against the main worktree", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		main := filepath.Join(root, "main")
		if err := os.MkdirAll(main, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, main)

		linked := filepath.Join(root, "linked")
		runGitCmd(t, main, "worktree", "add", "-b", "feature", linked)

		repos, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v", err)
		}

		wantMain, err := filepath.EvalSymlinks(main)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		wantLinked, err := filepath.EvalSymlinks(linked)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		want := []string{wantLinked, wantMain}
		sort.Strings(want)

		got := resolvedPaths(t, repos)
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("FindRepos() = %v, want %v", got, want)
		}
	})

	t.Run("deduplicates a repo reachable via more than one path under root", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		repoDir := filepath.Join(root, "real", "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, repoDir)

		alias := filepath.Join(root, "alias")
		if err := os.Symlink(repoDir, alias); err != nil {
			t.Skipf("symlinks not supported: %v", err)
		}

		repos, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v", err)
		}
		want, err := filepath.EvalSymlinks(repoDir)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		got := resolvedPaths(t, repos)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("FindRepos() = %v, want deduplicated [%q]", got, want)
		}
	})

	t.Run("excludes hidden directories, including .git internals", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		repoDir := filepath.Join(root, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, repoDir)

		// A repository nested inside a hidden directory must not be
		// discovered, and .git's internal worktree-like state (git
		// stores refs, objects, etc. as plain directories/files under
		// .git) must never be walked into or misreported as a repo.
		hidden := filepath.Join(root, ".hidden", "repo")
		if err := os.MkdirAll(hidden, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, hidden)

		repos, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v", err)
		}
		want, err := filepath.EvalSymlinks(repoDir)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		got := resolvedPaths(t, repos)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("FindRepos() = %v, want [%q] (hidden directories excluded)", got, want)
		}
	})

	t.Run("skips bare repositories without erroring", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		bareDir := filepath.Join(root, "bare")
		if err := os.MkdirAll(bareDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		runGitCmd(t, bareDir, "init", "--bare")

		nonBareDir := filepath.Join(root, "normal")
		if err := os.MkdirAll(nonBareDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, nonBareDir)

		repos, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v", err)
		}
		want, err := filepath.EvalSymlinks(nonBareDir)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		got := resolvedPaths(t, repos)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("FindRepos() = %v, want [%q] (bare repository skipped)", got, want)
		}
	})

	t.Run("is idempotent across repeated calls", func(t *testing.T) {
		requireGit(t)
		root := t.TempDir()
		repoDir := filepath.Join(root, "group", "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, repoDir)

		first, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v", err)
		}
		second, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v", err)
		}

		if got1, got2 := resolvedPaths(t, first), resolvedPaths(t, second); len(got1) != len(got2) {
			t.Fatalf("FindRepos() not idempotent: first=%v second=%v", got1, got2)
		} else {
			for i := range got1 {
				if got1[i] != got2[i] {
					t.Fatalf("FindRepos() not idempotent: first=%v second=%v", got1, got2)
				}
			}
		}
	})

	t.Run("unreadable directory is skipped rather than aborting the whole scan", func(t *testing.T) {
		requireGit(t)
		if os.Geteuid() == 0 {
			t.Skip("running as root: permission bits do not restrict access")
		}

		root := t.TempDir()

		unreadable := filepath.Join(root, "unreadable")
		if err := os.MkdirAll(unreadable, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		blocked := filepath.Join(unreadable, "repo")
		if err := os.MkdirAll(blocked, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, blocked)
		if err := os.Chmod(unreadable, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { os.Chmod(unreadable, 0o755) })

		reachable := filepath.Join(root, "reachable")
		if err := os.MkdirAll(reachable, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		initRepo(t, reachable)

		repos, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v, want scan to tolerate the unreadable directory", err)
		}
		want, err := filepath.EvalSymlinks(reachable)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		got := resolvedPaths(t, repos)
		if len(got) != 1 || got[0] != want {
			t.Fatalf("FindRepos() = %v, want only the reachable repo [%q] (unreadable subtree skipped, not fatal)", got, want)
		}
	})

	t.Run("empty root yields no repos and no error", func(t *testing.T) {
		root := t.TempDir()
		repos, err := FindRepos(root)
		if err != nil {
			t.Fatalf("FindRepos() error = %v", err)
		}
		if len(repos) != 0 {
			t.Fatalf("FindRepos() = %v, want empty", repos)
		}
	})
}

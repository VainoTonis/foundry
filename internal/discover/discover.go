// Package discover scans a filesystem tree for git repositories that can
// be imported as canonical repository.Repository rows.
package discover

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tonis2/foundry/internal/repository"
)

// Repo is a discovered git repository, deduplicated by its canonical
// worktree root.
type Repo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FindRepos recursively scans root for non-bare git worktrees, returning
// one Repo per canonical worktree root (repository.CanonicalLocalPath),
// deduplicated so a worktree reachable via more than one path under root
// (e.g. a symlink, or a linked worktree whose directory happens to also
// be nested elsewhere under root) is only reported once.
//
// Finding a repository at a given directory does not stop the walk from
// descending into it: nested and linked worktrees, and submodules, are
// legitimate independent repositories that can live inside another
// repository's working tree, and must still be discovered. Bare
// repositories, and any directory git does not recognize as a non-bare
// worktree, are silently skipped (but still descended into, in case a
// real worktree lives further down). A directory that cannot be read
// (permission error, or a race with concurrent deletion) is likewise
// skipped rather than aborting the whole scan.
//
// FindRepos never descends into a directory named ".git" (a worktree's
// own metadata, which cannot itself contain another repository a user
// would want imported) or into any other hidden ("dot") directory, on the
// same "never a repository worth discovering" assumption used by common
// repository scanners.
func FindRepos(root string) ([]Repo, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	var repos []Repo
	seen := make(map[string]bool)

	walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path != absRoot && excludedDirName(d.Name()) {
			return fs.SkipDir
		}

		if hasGitEntry(path) {
			if canonical, cErr := repository.CanonicalLocalPath(path); cErr == nil && !seen[canonical] {
				seen[canonical] = true
				name, relErr := filepath.Rel(absRoot, path)
				if relErr != nil {
					name = path
				}
				repos = append(repos, Repo{Name: name, Path: canonical})
			}
			// Whether or not this directory is (or resolves to) a valid
			// worktree, keep descending: it may contain further nested
			// or linked worktrees.
		}
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return repos, nil
}

// excludedDirName reports whether a directory named name is never
// descended into by FindRepos: ".git" itself, or any other hidden ("dot")
// directory.
func excludedDirName(name string) bool {
	return name == ".git" || (len(name) > 1 && name[0] == '.')
}

// hasGitEntry reports whether path contains a ".git" entry, regardless of
// whether it is a directory (a normal or main worktree) or a file (a
// linked worktree's or submodule's pointer to its real git directory).
func hasGitEntry(path string) bool {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	return err == nil
}

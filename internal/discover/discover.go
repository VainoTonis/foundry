package discover

import (
	"os"
	"path/filepath"
)

// Repo is a discovered git repository.
type Repo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// FindRepos scans root 2 levels deep for directories containing a .git folder.
// Structure expected: root/group/repo/.git
func FindRepos(root string) ([]Repo, error) {
	groups, err := readDirs(root)
	if err != nil {
		return nil, err
	}

	var repos []Repo
	for _, group := range groups {
		groupPath := filepath.Join(root, group)
		children, err := readDirs(groupPath)
		if err != nil {
			continue
		}
		for _, child := range children {
			repoPath := filepath.Join(groupPath, child)
			if isGitRepo(repoPath) {
				repos = append(repos, Repo{
					Name: group + "/" + child,
					Path: repoPath,
				})
			}
		}
		// also handle root/repo/.git (1-level, in case some live directly under root)
		if isGitRepo(groupPath) {
			repos = append(repos, Repo{
				Name: group,
				Path: groupPath,
			})
		}
	}
	return repos, nil
}

func readDirs(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".git" {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs, nil
}

func isGitRepo(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && info.IsDir()
}

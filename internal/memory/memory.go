package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Slice is the approved markdown memory loaded for a project namespace.
type Slice struct {
	RepoPath  string
	Namespace string
	Root      string
	Files     []File
	Markdown  string
}

type File struct {
	Path    string
	Content string
}

// LoadApproved loads the markdown memory for a project namespace from the
// configured memory repo. The namespace maps to a directory inside repoPath;
// all non-hidden .md files under that directory are considered approved memory.
func LoadApproved(repoPath, namespace string) (Slice, error) {
	repoPath = strings.TrimSpace(repoPath)
	namespace = strings.Trim(strings.TrimSpace(namespace), string(os.PathSeparator)+"/")
	out := Slice{RepoPath: repoPath, Namespace: namespace}
	if repoPath == "" || namespace == "" {
		return out, nil
	}

	root := filepath.Clean(filepath.Join(repoPath, filepath.FromSlash(namespace)))
	repoClean := filepath.Clean(repoPath)
	rel, err := filepath.Rel(repoClean, root)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return out, fmt.Errorf("invalid memory namespace %q", namespace)
	}
	out.Root = root

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	if !info.IsDir() {
		return out, nil
	}

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || (strings.HasPrefix(name, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".md") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out.Files = append(out.Files, File{Path: filepath.ToSlash(rel), Content: strings.TrimSpace(string(data))})
		return nil
	})
	if err != nil {
		return out, err
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	out.Markdown = format(out)
	return out, nil
}

func format(s Slice) string {
	if len(s.Files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Approved Project Memory\n\n")
	b.WriteString("Memory namespace: ")
	b.WriteString(s.Namespace)
	b.WriteString("\n\n")
	for i, f := range s.Files {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString("### ")
		b.WriteString(f.Path)
		b.WriteString("\n\n")
		b.WriteString(f.Content)
	}
	return b.String()
}

func Prepend(markdown, prompt string) string {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return prompt
	}
	if strings.HasPrefix(prompt, "## Approved Project Memory\n") {
		return prompt
	}
	return markdown + "\n\n---\n\n" + prompt
}

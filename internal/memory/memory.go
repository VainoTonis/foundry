package memory

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Slice is the approved markdown memory loaded for a project namespace.
type Slice struct {
	RepoPath  string
	Namespace string
	Root      string
	Files     []File
	Markdown  string
}

type Frontmatter struct {
	Title  string   `yaml:"title"`
	Tags   []string `yaml:"tags"`
	Always bool     `yaml:"always"`
}

type File struct {
	Path        string
	Frontmatter Frontmatter
	Content     string
}

// LoadApproved loads the markdown memory for a project namespace from the
// configured memory repo. The namespace maps to a directory inside repoPath;
// all non-hidden .md files under that directory are considered approved memory.
func LoadApproved(repoPath, namespace string, tags []string) (Slice, error) {
	repoPath = strings.TrimSpace(repoPath)
	namespace, nsErr := cleanNamespace(namespace)
	out := Slice{RepoPath: repoPath, Namespace: namespace}
	if nsErr != nil {
		return out, nsErr
	}
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
	if err := rejectSymlinkedNamespacePath(repoClean, namespace); err != nil {
		return out, err
	}

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
		if d.Type()&os.ModeSymlink != 0 {
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
		frontmatter, body := parseFrontmatter(strings.TrimSpace(string(data)))
		if !matchesTags(frontmatter, tags) {
			return nil
		}
		out.Files = append(out.Files, File{Path: filepath.ToSlash(rel), Frontmatter: frontmatter, Content: body})
		return nil
	})
	if err != nil {
		return out, err
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	out.Markdown = format(out)
	return out, nil
}

func parseFrontmatter(raw string) (Frontmatter, string) {
	if !strings.HasPrefix(raw, "---\n") {
		return Frontmatter{}, raw
	}
	idx := strings.Index(raw[4:], "\n---")
	if idx < 0 {
		return Frontmatter{}, raw
	}
	closingStart := 4 + idx
	closingEnd := closingStart + len("\n---")
	if closingEnd < len(raw) && raw[closingEnd] != '\n' {
		return Frontmatter{}, raw
	}
	yamlBlock := raw[4:closingStart]
	body := strings.TrimSpace(raw[closingEnd:])
	var fm Frontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return Frontmatter{}, body
	}
	return fm, body
}

func matchesTags(fm Frontmatter, tags []string) bool {
	if fm.Always || len(tags) == 0 {
		return true
	}
	for _, want := range tags {
		for _, have := range fm.Tags {
			if strings.EqualFold(want, have) {
				return true
			}
		}
	}
	return false
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
		title := f.Frontmatter.Title
		if title == "" {
			title = f.Path
		}
		b.WriteString(title)
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

func WriteApprovedUpdate(repoPath, namespace string, workflowID int64, markdown string) (string, error) {
	repoPath = strings.TrimSpace(repoPath)
	namespace, nsErr := cleanNamespace(namespace)
	if nsErr != nil {
		return "", nsErr
	}
	if repoPath == "" {
		return "", fmt.Errorf("memory repo path is not configured")
	}
	if namespace == "" {
		return "", fmt.Errorf("project memory namespace is not configured")
	}
	repoRoot := filepath.Clean(repoPath)
	dir := filepath.Clean(filepath.Join(repoRoot, filepath.FromSlash(namespace), "workflow-updates"))
	if rel, err := filepath.Rel(repoRoot, dir); err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("invalid memory namespace %q", namespace)
	}
	if err := rejectSymlinkedNamespacePath(repoRoot, namespace); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("workflow-%d.md", workflowID))
	if err := os.WriteFile(path, []byte(strings.TrimSpace(markdown)+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := commitFile(repoRoot, path, workflowID); err != nil {
		return "", err
	}
	return path, nil
}

func commitFile(repoRoot, path string, workflowID int64) error {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return err
	}
	add := exec.Command("git", "-C", repoRoot, "add", "--", rel)
	if out, err := add.CombinedOutput(); err != nil {
		return fmt.Errorf("git add memory update failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	commit := exec.Command("git", "-C", repoRoot, "commit", "-m", fmt.Sprintf("Accept memory update for workflow %d", workflowID), "--", rel)
	if out, err := commit.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit memory update failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func rejectSymlinkedNamespacePath(repoRoot, namespace string) error {
	cur := filepath.Clean(repoRoot)
	for _, part := range strings.Split(filepath.ToSlash(namespace), "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid memory namespace %q", namespace)
		}
	}
	return nil
}

func cleanNamespace(namespace string) (string, error) {
	namespace = strings.Trim(strings.TrimSpace(namespace), string(os.PathSeparator)+"/")
	if namespace == "" {
		return "", nil
	}
	if filepath.IsAbs(namespace) || filepath.IsAbs(filepath.FromSlash(namespace)) {
		return "", fmt.Errorf("invalid memory namespace %q", namespace)
	}
	for _, part := range strings.Split(filepath.ToSlash(namespace), "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("invalid memory namespace %q", namespace)
		}
	}
	return namespace, nil
}

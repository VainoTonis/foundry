package workflow

import (
	"context"
	"os/exec"
	"strings"
)

func cleanupCerberusGitState(ctx context.Context, repoPath, sessionName string) {
	_ = exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "remove", "--force",
		".cerberus/sessions/"+sessionName+"/worktrees/solve").Run()
	_ = exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "prune").Run()
	_ = exec.CommandContext(ctx, "git", "-C", repoPath, "branch", "-D", "cerberus/"+sessionName).Run()
}

func cerberusCommitHash(ctx context.Context, repoPath, sessionName string) string {
	hashOut, err := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "cerberus/"+sessionName).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(hashOut))
}

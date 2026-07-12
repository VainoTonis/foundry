package workflow

import (
	"fmt"
	"strings"

	"github.com/tonis2/foundry/internal/spec"
)

const repoRootPromptHeader = `## Target Repository Root

You are running in the configured target repository root: %s
Treat the current working directory as the workspace root. All file paths in the spec are relative to this root. If the spec mentions the repository directory name, do not create a nested copy of that directory; modify files in this root.

---

`

func buildPhasePrompt(repoPath, globalCtx, phaseGoal, trackOverlay string, adjustedPrompt *string) string {
	prompt := spec.BuildPrompt(globalCtx, phaseGoal, trackOverlay)
	if adjustedPrompt != nil && *adjustedPrompt != "" {
		prompt = *adjustedPrompt
	}
	return prependRepoRootContext(repoPath, prompt)
}

func prependRepoRootContext(repoPath, prompt string) string {
	if strings.HasPrefix(prompt, "## Target Repository Root\n") {
		return prompt
	}
	return fmt.Sprintf(repoRootPromptHeader, repoPath) + prompt
}

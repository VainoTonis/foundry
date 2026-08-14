package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/tonis2/foundry/internal/db"
)

const (
	knowledgeVaultPathSetting = "knowledge_vault_path"
	vaultContainerPath        = "/vault"
)

func profileFilePath(session string) string {
	return "/tmp/foundry-profile-" + session + ".json"
}

func removeProfileFile(session string) {
	os.Remove(profileFilePath(session))
}

// buildProfilePayload assembles the JSON payload written to the cerberus
// profile file for the given profile (may be nil) and knowledge vault path
// (may be empty, meaning the vault feature is disabled). It returns the
// payload map and whether a vault mount was included.
func buildProfilePayload(p *db.Profile, vaultPath string) (map[string]any, bool) {
	payload := map[string]any{}
	if p != nil {
		if p.DefaultModel != "" {
			payload["default_model"] = p.DefaultModel
		}
		if p.DefaultImage != "" {
			payload["default_image"] = p.DefaultImage
		}
		if p.AWSProfile != "" {
			payload["aws_profile"] = p.AWSProfile
		}
		if p.AWSRegion != "" {
			payload["aws_region"] = p.AWSRegion
		}
		if len(p.ExtraEnv) > 0 {
			payload["extra_env"] = p.ExtraEnv
		}
	}
	if vaultPath == "" {
		return payload, false
	}
	payload["extra_mounts"] = []map[string]any{
		{"host": vaultPath, "container": vaultContainerPath, "read_only": true},
	}
	return payload, true
}

// vaultInstructions builds a short prompt addendum describing how to use the
// mounted knowledge vault, mirroring the "Attached project dirs" style used
// by internal/chat/service.go.
func vaultInstructions() string {
	return strings.Join([]string{
		"Knowledge vault mounted (read-only) at " + vaultContainerPath + ":",
		"  Use `frontmatter-radar query <term>` to search vault notes.",
		"  Use `frontmatter-radar read <path>` to read a specific vault file.",
	}, "\n")
}

// writeProfileFile writes a cerberus profile file for the given profile
// name/session. If the profile name is empty, no profile fields are
// included, but a knowledge vault mount is still attached when the
// "knowledge_vault_path" app setting is configured (non-empty). It returns
// the written profile path (empty if there was nothing to write) and
// whether a vault mount was included.
func (r *Runner) writeProfileFile(ctx context.Context, profileName, session string) (string, bool, error) {
	var p *db.Profile
	if profileName != "" {
		found, err := db.GetProfileByName(ctx, r.pool, profileName)
		if err != nil && err != db.ErrNotFound {
			return "", false, fmt.Errorf("lookup profile %q: %w", profileName, err)
		}
		if err == nil {
			p = &found
		}
	}

	vaultPath := ""
	setting, err := db.GetAppSetting(ctx, r.pool, knowledgeVaultPathSetting)
	if err != nil && err != db.ErrNotFound {
		return "", false, fmt.Errorf("lookup knowledge vault path setting: %w", err)
	}
	if err == nil {
		vaultPath = setting.Value
	}

	payload, vaultMounted := buildProfilePayload(p, vaultPath)
	if len(payload) == 0 {
		return "", false, nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", false, fmt.Errorf("marshal profile: %w", err)
	}
	path := profileFilePath(session)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", false, fmt.Errorf("write profile file: %w", err)
	}
	return path, vaultMounted, nil
}

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tonis2/foundry/internal/db"
)

func profileFilePath(session string) string {
	return "/tmp/foundry-profile-" + session + ".json"
}

func removeProfileFile(session string) {
	os.Remove(profileFilePath(session))
}

// buildProfilePayload assembles the JSON payload written to the cerberus
// profile file for the given profile (may be nil).
func buildProfilePayload(p *db.Profile) map[string]any {
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
	return payload
}

// writeProfileFile writes a cerberus profile file for the given profile
// name/session. If the profile name is empty, no profile fields are
// included and nothing is written. It returns the written profile path
// (empty if there was nothing to write).
func (r *Runner) writeProfileFile(ctx context.Context, profileName, session string) (string, error) {
	var p *db.Profile
	if profileName != "" {
		found, err := db.GetProfileByName(ctx, r.pool, profileName)
		if err != nil && err != db.ErrNotFound {
			return "", fmt.Errorf("lookup profile %q: %w", profileName, err)
		}
		if err == nil {
			p = &found
		}
	}

	payload := buildProfilePayload(p)
	if len(payload) == 0 {
		return "", nil
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal profile: %w", err)
	}
	path := profileFilePath(session)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write profile file: %w", err)
	}
	return path, nil
}

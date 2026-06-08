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

func (r *Runner) writeProfileFile(ctx context.Context, profileName, session string) (string, error) {
	if profileName == "" {
		return "", nil
	}
	p, err := db.GetProfileByName(ctx, r.pool, profileName)
	if err == db.ErrNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("lookup profile %q: %w", profileName, err)
	}
	payload := map[string]any{}
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

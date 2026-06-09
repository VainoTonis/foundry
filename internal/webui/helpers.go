package webui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tonis2/foundry/internal/config"
)

func parseUIID(path, prefix string) (id int64, fragment bool, ok bool) {
	id, suffix, ok := parseUIIDSuffix(path, prefix)
	return id, suffix == "fragment", ok && (suffix == "" || suffix == "fragment")
}

func parseUIIDSuffix(path, prefix string) (int64, string, bool) {
	if !strings.HasPrefix(path, prefix) {
		return 0, "", false
	}
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" {
		return 0, "", false
	}
	parts := strings.SplitN(rest, "/", 2)
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	if len(parts) == 1 {
		return id, "", true
	}
	return id, parts[1], true
}

func isRuntimeSetting(key string) bool { return config.RuntimeSettingKeys()[key] }

func mergeYAMLRuntimeSettings(yaml string, values map[string]string) string {
	patch := make(map[string]any, len(values))
	for k, v := range values {
		patch[k] = v
	}
	return applyYAMLPatch(yaml, patch)
}

func applyYAMLPatch(yaml string, patch map[string]any) string {
	lines := strings.Split(yaml, "\n")
	replaced := map[string]bool{}
	for i, line := range lines {
		for k, v := range patch {
			prefix := k + ":"
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				lines[i] = fmt.Sprintf("%s: %s", k, yamlValue(v))
				replaced[k] = true
			}
		}
	}
	for k, v := range patch {
		if !replaced[k] {
			lines = append(lines, fmt.Sprintf("%s: %s", k, yamlValue(v)))
		}
	}
	return strings.Join(lines, "\n")
}

func yamlValue(v any) string {
	s := fmt.Sprint(v)
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	if _, err := strconv.ParseBool(s); err == nil {
		return s
	}
	return fmt.Sprintf("%q", s)
}

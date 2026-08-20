package db

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSpecJSONUsesRepositoryIDExternally locks in that Spec's external
// JSON representation exposes ownership as "repository_id", matching the
// underlying SQL column backing it, repository_id. The Go field is named
// RepositoryID and must serialize under that name, with no lingering
// legacy ownership key from before the Project -> Repository rename.
func TestSpecJSONUsesRepositoryIDExternally(t *testing.T) {
	s := Spec{ID: 1, Title: "t", RepositoryID: 42}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"repository_id":42`) {
		t.Fatalf("Spec JSON = %s, want repository_id field", b)
	}
	legacyKey := strings.Join([]string{"project", "id"}, "_")
	if strings.Contains(string(b), legacyKey) {
		t.Fatalf("Spec JSON = %s, must not contain legacy %s key", b, legacyKey)
	}
}

// TestSpecDraftJSONUsesRepositoryIDExternally is the SpecDraft analogue of
// TestSpecJSONUsesRepositoryIDExternally: ownership must be externally
// named repository_id (and remain nullable, since a draft may not yet be
// attached to a repository), matching the underlying repository_id
// column, never the legacy ownership key from before the rename.
func TestSpecDraftJSONUsesRepositoryIDExternally(t *testing.T) {
	id := int64(7)
	d := SpecDraft{ID: 1, RepositoryID: &id, Title: "t"}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"repository_id":7`) {
		t.Fatalf("SpecDraft JSON = %s, want repository_id field", b)
	}
	legacyKey := strings.Join([]string{"project", "id"}, "_")
	if strings.Contains(string(b), legacyKey) {
		t.Fatalf("SpecDraft JSON = %s, must not contain legacy %s key", b, legacyKey)
	}
}

func TestIsSpecDraftSafeToCleanStatusUsesMigratedTerminalStatuses(t *testing.T) {
	for _, status := range []string{SpecDraftStatusFrozen, SpecDraftStatusError} {
		if !IsSpecDraftSafeToCleanStatus(status) {
			t.Fatalf("status %q should be safe to clean", status)
		}
	}

	for _, status := range []string{"saved", "active", "ready_to_freeze", "abandoned", ""} {
		if IsSpecDraftSafeToCleanStatus(status) {
			t.Fatalf("status %q should not be considered safe to clean", status)
		}
	}
}

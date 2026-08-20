package db

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSpecJSONUsesRepositoryIDExternally locks in that Spec's external
// JSON representation exposes ownership as "repository_id", even though
// the underlying SQL column backing it remains project_id. This is the
// externally-facing rename this slice performs: the Go field is named
// RepositoryID and must serialize under that name, with no lingering
// "project_id" key.
func TestSpecJSONUsesRepositoryIDExternally(t *testing.T) {
	s := Spec{ID: 1, Title: "t", RepositoryID: 42}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"repository_id":42`) {
		t.Fatalf("Spec JSON = %s, want repository_id field", b)
	}
	if strings.Contains(string(b), "project_id") {
		t.Fatalf("Spec JSON = %s, must not contain legacy project_id key", b)
	}
}

// TestSpecDraftJSONUsesRepositoryIDExternally is the SpecDraft analogue of
// TestSpecJSONUsesRepositoryIDExternally: ownership must be externally
// named repository_id (and remain nullable, since a draft may not yet be
// attached to a repository), never project_id.
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
	if strings.Contains(string(b), "project_id") {
		t.Fatalf("SpecDraft JSON = %s, must not contain legacy project_id key", b)
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

package db

import "testing"

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

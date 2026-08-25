package httpserver

import (
	"testing"

	"github.com/tonis2/foundry/internal/cerberus"
)

func TestResolveReviewModel(t *testing.T) {
	t.Run("explicit review_model wins over Cerberus's default model", func(t *testing.T) {
		cerb := cerberus.New("cerberus", "", "claude-cerberus-default", "")
		got := resolveReviewModel("gpt-review", cerb)
		if got != "gpt-review" {
			t.Fatalf("resolveReviewModel() = %q, want explicit override gpt-review", got)
		}
	})

	t.Run("omitted review_model falls back to Cerberus default-model resolution", func(t *testing.T) {
		cerb := cerberus.New("cerberus", "", "claude-cerberus-default", "")
		got := resolveReviewModel("", cerb)
		if got != "claude-cerberus-default" {
			t.Fatalf("resolveReviewModel() = %q, want Cerberus default claude-cerberus-default", got)
		}
	})

	t.Run("omitted review_model and no Cerberus default stays empty for cerberus to pick", func(t *testing.T) {
		cerb := cerberus.New("cerberus", "", "", "")
		got := resolveReviewModel("", cerb)
		if got != "" {
			t.Fatalf("resolveReviewModel() = %q, want empty", got)
		}
	})

	t.Run("nil client with omitted review_model stays empty", func(t *testing.T) {
		got := resolveReviewModel("", nil)
		if got != "" {
			t.Fatalf("resolveReviewModel() = %q, want empty", got)
		}
	})
}

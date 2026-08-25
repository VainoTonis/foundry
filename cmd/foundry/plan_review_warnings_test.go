package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestPrintReviewWarnings covers every shape runCmd's review_warnings
// field can arrive in from a decoded map[string]any response: absent,
// empty, well-formed, and malformed entries, none of which must ever
// write anything but advisory lines to stderr.
func TestPrintReviewWarnings(t *testing.T) {
	cases := []struct {
		name       string
		raw        any
		wantEmpty  bool
		wantLines  []string
		wantAbsent []string
	}{
		{name: "nil field", raw: nil, wantEmpty: true},
		{name: "wrong type", raw: "not a list", wantEmpty: true},
		{name: "empty list", raw: []any{}, wantEmpty: true},
		{
			name: "well-formed warnings",
			raw: []any{
				map[string]any{"code": "review_missing", "message": "no Steward review has ever been run for this plan"},
				map[string]any{"code": "review_stale", "message": "the most recent Steward review no longer matches the plan's current content"},
			},
			wantLines: []string{
				"[review_missing] no Steward review has ever been run for this plan",
				"[review_stale] the most recent Steward review no longer matches the plan's current content",
			},
		},
		{
			name: "malformed entries are skipped, not fatal",
			raw: []any{
				"not a map",
				map[string]any{"code": "review_failed", "message": "the most recent Steward review failed to complete"},
			},
			wantLines:  []string{"[review_failed] the most recent Steward review failed to complete"},
			wantAbsent: []string{"not a map"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printReviewWarnings(&buf, tc.raw)
			out := buf.String()
			if tc.wantEmpty {
				if out != "" {
					t.Fatalf("printReviewWarnings(%v) wrote %q, want empty", tc.raw, out)
				}
				return
			}
			for _, want := range tc.wantLines {
				if !strings.Contains(out, want) {
					t.Fatalf("printReviewWarnings output %q does not contain %q", out, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(out, absent) {
					t.Fatalf("printReviewWarnings output %q unexpectedly contains %q", out, absent)
				}
			}
		})
	}
}

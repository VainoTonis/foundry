package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestTruncateContent(t *testing.T) {
	asciiLarge := strings.Repeat("a", 300*1024)
	multibyteLarge := strings.Repeat("\u00e9", 150*1024)
	short := "hello world"

	cases := []struct {
		name          string
		in            string
		wantTruncated bool
	}{
		{"300 KiB ASCII", asciiLarge, true},
		{"300 KiB multibyte", multibyteLarge, true},
		{"short content unchanged", short, false},
		{"empty content unchanged", "", false},
		{"exactly at cap unchanged", strings.Repeat("a", MaxContentBytes), false},
		{"one byte over cap truncated", strings.Repeat("a", MaxContentBytes+1), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, truncated, sha, origBytes := truncateContent(tc.in)

			if truncated != tc.wantTruncated {
				t.Fatalf("truncated = %v, want %v", truncated, tc.wantTruncated)
			}

			if !tc.wantTruncated {
				if out != tc.in {
					t.Fatalf("out = %q, want unchanged input", out)
				}
				if sha != nil {
					t.Fatalf("sha = %v, want nil for non-truncated content", sha)
				}
				if origBytes != nil {
					t.Fatalf("origBytes = %v, want nil for non-truncated content", origBytes)
				}
				return
			}

			if len(out) > MaxContentBytes {
				t.Fatalf("len(out) = %d, want <= %d", len(out), MaxContentBytes)
			}
			if !isValidUTF8Boundary(tc.in, len(out)) {
				t.Fatalf("truncation of %q at byte %d does not land on a UTF-8 rune boundary", tc.name, len(out))
			}
			if !validUTF8(out) {
				t.Fatalf("truncated output for %q is not valid UTF-8", tc.name)
			}

			if sha == nil {
				t.Fatal("sha = nil, want set for truncated content")
			}
			wantSum := sha256.Sum256([]byte(tc.in))
			wantHash := hex.EncodeToString(wantSum[:])
			if *sha != wantHash {
				t.Fatalf("sha = %q, want %q", *sha, wantHash)
			}

			if origBytes == nil {
				t.Fatal("origBytes = nil, want set for truncated content")
			}
			if *origBytes != int64(len(tc.in)) {
				t.Fatalf("origBytes = %d, want %d", *origBytes, len(tc.in))
			}
		})
	}
}

func isValidUTF8Boundary(s string, at int) bool {
	if at == len(s) {
		return true
	}
	return s[at]&0xC0 != 0x80
}

func validUTF8(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

func TestContentFields(t *testing.T) {
	t.Run("empty content stores nothing", func(t *testing.T) {
		content, truncated, sha, origBytes := contentFields("")
		if content != nil || truncated || sha != nil || origBytes != nil {
			t.Fatalf("contentFields(\"\") = (%v, %v, %v, %v), want all zero", content, truncated, sha, origBytes)
		}
	})

	t.Run("short content stores verbatim", func(t *testing.T) {
		content, truncated, sha, origBytes := contentFields("short")
		if content == nil || *content != "short" {
			t.Fatalf("content = %v, want \"short\"", content)
		}
		if truncated {
			t.Fatal("truncated = true, want false")
		}
		if sha != nil || origBytes != nil {
			t.Fatalf("sha = %v, origBytes = %v, want nil for non-truncated content", sha, origBytes)
		}
	})

	t.Run("oversized content is truncated and hashed", func(t *testing.T) {
		large := strings.Repeat("z", MaxContentBytes+10)
		content, truncated, sha, origBytes := contentFields(large)
		if !truncated {
			t.Fatal("truncated = false, want true")
		}
		if content == nil || len(*content) > MaxContentBytes {
			t.Fatalf("content len = %d, want <= %d", len(*content), MaxContentBytes)
		}
		if sha == nil || origBytes == nil || *origBytes != int64(len(large)) {
			t.Fatalf("sha = %v, origBytes = %v, want set with %d", sha, origBytes, len(large))
		}
	})
}

func TestValidateEvent(t *testing.T) {
	valid := func(mutate func(*Event)) Event {
		ev := Event{
			Type: EventSessionStart,
			Session: Session{
				Session:         "s1",
				SourceSessionID: "src-1",
				Origin:          "claude-code",
			},
		}
		if mutate != nil {
			mutate(&ev)
		}
		return ev
	}

	cases := []struct {
		name    string
		event   Event
		wantErr bool
	}{
		{"valid session_start", valid(nil), false},
		{"empty session rejected", valid(func(e *Event) { e.Session.Session = "" }), true},
		{"session_start missing source_session_id rejected", valid(func(e *Event) { e.Session.SourceSessionID = "" }), true},
		{"session_start missing origin rejected", valid(func(e *Event) { e.Session.Origin = "" }), true},
		{"unknown event type rejected", valid(func(e *Event) { e.Type = "not-a-real-event" }), true},
		{"delta events rejected", valid(func(e *Event) { e.Type = EventDelta }), true},
		{
			"valid tool_use",
			Event{Type: EventToolUse, Session: Session{Session: "s1"}, Tool: Tool{ToolName: "bash"}},
			false,
		},
		{
			"tool_use missing tool name rejected",
			Event{Type: EventToolUse, Session: Session{Session: "s1"}},
			true,
		},
		{
			"valid tool_result",
			Event{Type: EventToolResult, Session: Session{Session: "s1"}, Tool: Tool{ToolName: "bash"}},
			false,
		},
		{
			"tool_result missing tool name rejected",
			Event{Type: EventToolResult, Session: Session{Session: "s1"}},
			true,
		},
		{
			"valid final_message",
			Event{Type: EventFinalMessage, Session: Session{Session: "s1"}, Message: Message{Role: "assistant"}},
			false,
		},
		{
			"final_message missing role rejected",
			Event{Type: EventFinalMessage, Session: Session{Session: "s1"}},
			true,
		},
		{
			"valid message_end with zero usage",
			Event{Type: EventMessageEnd, Session: Session{Session: "s1"}},
			false,
		},
		{
			"valid session_end",
			Event{Type: EventSessionEnd, Session: Session{Session: "s1"}},
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEvent(tc.event)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateEvent() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name      string
		event     Event
		wantStore bool
		wantErr   bool
	}{
		{
			"zero usage message_end is valid but not stored",
			Event{Type: EventMessageEnd, Session: Session{Session: "s1"}},
			false,
			false,
		},
		{
			"nonzero input tokens message_end is stored",
			Event{Type: EventMessageEnd, Session: Session{Session: "s1"}, Usage: Usage{InputTokens: 1}},
			true,
			false,
		},
		{
			"nonzero cost message_end is stored",
			Event{Type: EventMessageEnd, Session: Session{Session: "s1"}, Usage: Usage{CostUSD: 0.01}},
			true,
			false,
		},
		{
			"invalid event is never stored",
			Event{Type: "bogus", Session: Session{Session: "s1"}},
			false,
			true,
		},
		{
			"tool_use with tool name is stored",
			Event{Type: EventToolUse, Session: Session{Session: "s1"}, Tool: Tool{ToolName: "bash"}},
			true,
			false,
		},
		{
			"session_start is stored",
			Event{Type: EventSessionStart, Session: Session{Session: "s1", SourceSessionID: "src", Origin: "cli"}},
			true,
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := classify(tc.event)
			if (err != nil) != tc.wantErr {
				t.Fatalf("classify() error = %v, wantErr %v", err, tc.wantErr)
			}
			if store != tc.wantStore {
				t.Fatalf("classify() store = %v, want %v", store, tc.wantStore)
			}
		})
	}
}

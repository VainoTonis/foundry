package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleKnowledgeFeedbackMissingEvidence(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/knowledge-feedback", bytes.NewBufferString(`{"kind":"stale","note_path":"a.md"}`))
	rec := httptest.NewRecorder()

	h.HandleKnowledgeFeedback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleKnowledgeFeedbackInvalidKind(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/knowledge-feedback", bytes.NewBufferString(`{"kind":"bogus","note_path":"a.md","evidence":"e"}`))
	rec := httptest.NewRecorder()

	h.HandleKnowledgeFeedback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleKnowledgeFeedbackMissingNotePathForStale(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/knowledge-feedback", bytes.NewBufferString(`{"kind":"stale","evidence":"e"}`))
	rec := httptest.NewRecorder()

	h.HandleKnowledgeFeedback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleKnowledgeFeedbackMissingNotePathAllowedForGap(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/knowledge-feedback", bytes.NewBufferString(`{"kind":"gap","evidence":"e"}`))
	rec := httptest.NewRecorder()

	pastValidation := false
	func() {
		defer func() {
			// A panic here means validation passed and execution reached the
			// (nil) db pool call, which is expected in this unit test.
			recover()
			pastValidation = true
		}()
		h.HandleKnowledgeFeedback(rec, req)
	}()

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("gap kind without note_path should not be a validation error, got 400: %s", rec.Body.String())
	}
	if !pastValidation {
		t.Fatalf("expected handler to proceed past validation")
	}
}

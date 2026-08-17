package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleFeedbacksLegacyMissingBody(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback", bytes.NewBufferString(`{"model":"m","session_id":"s"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredMissingDimension(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"target":"session-1","score":4,"evidence":"looks good"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredInvalidDimension(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"dimension":"bogus","target":"session-1","score":4,"evidence":"e"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredMissingTarget(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"dimension":"tests","score":4,"evidence":"e"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredScoreOutOfRange(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"dimension":"tests","target":"session-1","score":9,"evidence":"e"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredMissingEvidence(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"dimension":"tests","target":"session-1","score":4}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredInvalidImpact(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"dimension":"tests","target":"session-1","score":4,"evidence":"e","impact":"nope"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredInvalidStatus(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"dimension":"tests","target":"session-1","score":4,"evidence":"e","status":"bogus"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbackByIDInvalidID(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPatch, "/api/feedback/not-a-number", bytes.NewBufferString(`{"status":"resolved"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbackByID(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbackByIDMissingStatus(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPatch, "/api/feedback/1", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbackByID(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbackByIDInvalidStatus(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPatch, "/api/feedback/1", bytes.NewBufferString(`{"status":"bogus"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbackByID(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbackByIDMethodNotAllowed(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/feedback/1", nil)
	rec := httptest.NewRecorder()

	h.HandleFeedbackByID(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

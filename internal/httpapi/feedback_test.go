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

func TestHandleFeedbacksLegacyMissingRepositoryIDs(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback", bytes.NewBufferString(`{"body":"b","model":"m","session_id":"s"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredMissingRepositoryIDs(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"body":"b","dimension":"prompt_quality","target":"orchestrator_prompt","score":4,"evidence":"looks good"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredMissingBody(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"dimension":"prompt_quality","target":"orchestrator_prompt","score":4,"evidence":"looks good"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredMissingDimension(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"body":"b","target":"orchestrator_prompt","score":4,"evidence":"looks good"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredInvalidDimension(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"body":"b","dimension":"bogus","target":"orchestrator_prompt","score":4,"evidence":"e"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredMissingTarget(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"body":"b","dimension":"tool_usage","score":4,"evidence":"e"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredInvalidTarget(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"body":"b","dimension":"tool_usage","target":"bogus","score":4,"evidence":"e"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredScoreOutOfRange(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"body":"b","dimension":"tool_usage","target":"tool_flow","score":9,"evidence":"e"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredFreeProseImpactWithInvalidDimensionStillFailsOnDimension(t *testing.T) {
	h := New(nil, Config{})
	// Impact is free prose now (no enum check). This request uses an
	// arbitrary, non-enum impact string alongside an invalid dimension, and
	// must still be rejected for the dimension, not the impact value.
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"body":"b","dimension":"bogus","target":"outcome","score":4,"impact":"caused a flaky retry loop in phase 3"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbacksStructuredInvalidStatus(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPost, "/api/feedback",
		bytes.NewBufferString(`{"body":"b","dimension":"tool_usage","target":"tool_flow","score":4,"evidence":"e","status":"bogus"}`))
	rec := httptest.NewRecorder()

	h.HandleFeedbacks(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleFeedbackByIDInvalidID(t *testing.T) {
	h := New(nil, Config{})
	req := httptest.NewRequest(http.MethodPatch, "/api/feedback/not-a-number", bytes.NewBufferString(`{"status":"applied"}`))
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

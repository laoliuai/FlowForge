package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/config"
)

type healthResponse struct {
	Status string `json:"status"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func TestHealthEndpoint(t *testing.T) {
	cfg := &config.Config{}
	server := NewServer(nil, nil, cfg, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()

	server.Router().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var response healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.Status != "ok" {
		t.Fatalf("expected status ok, got %q", response.Status)
	}
}

func TestAPIAuthRequired(t *testing.T) {
	cfg := &config.Config{}
	server := NewServer(nil, nil, cfg, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	recorder := httptest.NewRecorder()

	server.Router().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, recorder.Code)
	}

	var response errorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if response.Error != "missing authorization" {
		t.Fatalf("expected missing authorization error, got %q", response.Error)
	}
}

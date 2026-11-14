package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
)

func TestHealthFeaturesStatusAndStaticUI(t *testing.T) {
	t.Setenv("GATEWAY_MODE", "standalone")
	t.Setenv("ENABLE_WOL", "false")
	cfg := config.Load()
	mux := buildMux(cfg)

	for _, tc := range []struct {
		path     string
		wantCode int
		wantType string
	}{
		{"/api/health", http.StatusOK, "application/json"},
		{"/api/features", http.StatusOK, "application/json"},
		{"/api/status", http.StatusOK, "application/json"},
		{"/", http.StatusOK, "text/html"},
		{"/manifest.json", http.StatusOK, "application/manifest+json"},
		{"/static/icon.svg", http.StatusOK, "image/svg+xml"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != tc.wantCode {
			t.Fatalf("GET %s: got status %d, want %d", tc.path, rec.Code, tc.wantCode)
		}
		if tc.path == "/api/features" {
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid features JSON: %v", err)
			}
			if body["mode"] != "standalone" {
				t.Fatalf("expected standalone mode, got %v", body["mode"])
			}
		}
		if tc.path == "/api/status" {
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid status JSON: %v", err)
			}
			if body["online"] != true {
				t.Fatalf("expected standalone online=true, got %v", body["online"])
			}
		}
	}
}

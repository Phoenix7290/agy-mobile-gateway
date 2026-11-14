package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/db"
)

func newTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	t.Setenv("GATEWAY_MODE", "standalone")
	t.Setenv("ENABLE_WOL", "false")
	tmpDir := t.TempDir()
	t.Setenv("DATA_DIR", tmpDir)

	cfg := config.Load()
	store, err := db.Open(filepath.Join(tmpDir, "gateway.db"))
	if err != nil {
		t.Fatalf("db.Open failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return buildMux(cfg, store)
}

func TestHealthFeaturesStatusAndStaticUI(t *testing.T) {
	mux := newTestMux(t)

	for _, tc := range []struct {
		path     string
		wantCode int
	}{
		{"/api/health", http.StatusOK},
		{"/api/features", http.StatusOK},
		{"/api/status", http.StatusOK},
		{"/api/settings", http.StatusOK},
		{"/", http.StatusOK},
		{"/manifest.json", http.StatusOK},
		{"/static/icon.svg", http.StatusOK},
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

func TestSettingsEndpointPutAndValidation(t *testing.T) {
	mux := newTestMux(t)

	// Valid update
	payload := `{"default_permission_mode":"autonomous","default_model":"auto"}`
	req := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(payload))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if updated["default_permission_mode"] != "autonomous" || updated["default_model"] != "auto" {
		t.Fatalf("unexpected settings payload: %+v", updated)
	}

	// Invalid permission mode should return 400
	badReq := httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"default_permission_mode":"root_all"}`))
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid permission mode, got %d", badRec.Code)
	}
}

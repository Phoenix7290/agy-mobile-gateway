package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/db"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/jobs"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/runner"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/wol"
)

func newTestServer(t *testing.T) (*http.ServeMux, *db.GatewayDB, config.GatewaySettings) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("GATEWAY_MODE", "standalone")
	t.Setenv("ENABLE_WOL", "false")
	t.Setenv("DATA_DIR", tmpDir)
	t.Setenv("CACHE_DIR", filepath.Join(tmpDir, "cache"))
	t.Setenv("INBOX_DIR", filepath.Join(tmpDir, "inbox"))
	t.Setenv("OUTBOX_DIR", filepath.Join(tmpDir, "outbox"))
	t.Setenv("AGY_BRAIN_DIR", filepath.Join(tmpDir, "brain"))

	cfg := config.Load()
	store, err := db.Open(filepath.Join(tmpDir, "gateway.db"))
	if err != nil {
		t.Fatalf("db.Open failed: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	r := runner.Create(cfg)
	mgr := jobs.NewManager(cfg, store, r)
	wd := &watchdogState{}
	return buildMux(cfg, store, r, mgr, wd), store, cfg
}

func TestHealthFeaturesStatusModelsAndSettings(t *testing.T) {
	mux, _, _ := newTestServer(t)

	for _, path := range []string{"/api/health", "/api/features", "/api/status", "/api/watchdog", "/api/telemetry", "/api/settings", "/api/models", "/api/jobs/active", "/api/outbox", "/", "/manifest.json", "/static/icon.svg"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: got status %d, want 200", path, rec.Code)
		}
	}

	// Update settings + validation
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"default_permission_mode":"autonomous","default_model":"auto"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings expected 200, got %d", rec.Code)
	}
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"default_permission_mode":"invalid"}`)))
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid permission mode, got %d", badRec.Code)
	}
}

func TestInboxUploadOutboxAndWoLPacket(t *testing.T) {
	mux, _, cfg := newTestServer(t)

	// 1. Multipart upload -> saved in Inbox
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "relatório teste!.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(fw, "conteudo inbox")
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/upload status %d: %s", rec.Code, rec.Body.String())
	}
	var upRes map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &upRes)
	if upRes["ok"] != true {
		t.Fatalf("unexpected upload payload: %+v", upRes)
	}

	// 2. Outbox list, download, delete
	if err := os.WriteFile(filepath.Join(cfg.Dirs.OutboxDir, "saida.txt"), []byte("ola outbox"), 0o644); err != nil {
		t.Fatal(err)
	}
	lsRec := httptest.NewRecorder()
	mux.ServeHTTP(lsRec, httptest.NewRequest(http.MethodGet, "/api/outbox", nil))
	if !strings.Contains(lsRec.Body.String(), "saida.txt") {
		t.Fatalf("expected saida.txt in outbox list: %s", lsRec.Body.String())
	}

	dlRec := httptest.NewRecorder()
	mux.ServeHTTP(dlRec, httptest.NewRequest(http.MethodGet, "/api/outbox/saida.txt", nil))
	if dlRec.Code != http.StatusOK || dlRec.Body.String() != "ola outbox" {
		t.Fatalf("unexpected outbox download: code=%d body=%q", dlRec.Code, dlRec.Body.String())
	}

	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, httptest.NewRequest(http.MethodDelete, "/api/outbox/saida.txt", nil))
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/outbox status %d", delRec.Code)
	}

	// 3. WoL disabled check + Magic Packet structure (102 bytes)
	wolRec := httptest.NewRecorder()
	mux.ServeHTTP(wolRec, httptest.NewRequest(http.MethodPost, "/api/wol", nil))
	if wolRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when WoL disabled, got %d", wolRec.Code)
	}
	pkt, err := wol.BuildMagicPacket("bc:fc:e7:ca:84:05")
	if err != nil || len(pkt) != 102 {
		t.Fatalf("unexpected magic packet len=%d err=%v", len(pkt), err)
	}
}

func TestConversationsSearchAndJobSSEStreaming(t *testing.T) {
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = io.WriteString(w, `{"models":[{"name":"ryan-fast:8b"}]}`)
		case "/api/chat":
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = io.WriteString(w, "{\"message\":{\"content\":\"DNS \"},\"done\":false}\n")
			_, _ = io.WriteString(w, "{\"message\":{\"content\":\"resolve nomes.\"},\"done\":true}\n")
		}
	}))
	defer ollamaSrv.Close()

	t.Setenv("OLLAMA_URL", ollamaSrv.URL)
	mux, _, _ := newTestServer(t)

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/jobs", "application/json", strings.NewReader(`{"prompt":"o que e DNS?","model":"ollama:ryan-fast:8b"}`))
	if err != nil {
		t.Fatalf("POST /api/jobs failed: %v", err)
	}
	defer resp.Body.Close()

	var created struct {
		JobID          string `json:"job_id"`
		ConversationID string `json:"conversation_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	sseResp, err := http.Get(ts.URL + "/api/jobs/" + created.JobID + "/events?after=0")
	if err != nil {
		t.Fatal(err)
	}
	defer sseResp.Body.Close()
	sseBytes, _ := io.ReadAll(sseResp.Body)
	sseText := string(sseBytes)
	if !strings.Contains(sseText, "DNS ") || !strings.Contains(sseText, "resolve nomes.") || !strings.Contains(sseText, "data: [DONE]") {
		t.Fatalf("unexpected SSE output: %s", sseText)
	}

	convResp, err := http.Get(ts.URL + "/api/conversations?q=DNS")
	if err != nil {
		t.Fatal(err)
	}
	defer convResp.Body.Close()
	var listBody struct {
		Total         int `json:"total"`
		Conversations []struct {
			ID string `json:"id"`
		} `json:"conversations"`
	}
	_ = json.NewDecoder(convResp.Body).Decode(&listBody)
	if listBody.Total != 1 || listBody.Conversations[0].ID != created.ConversationID {
		t.Fatalf("expected 1 matching FTS conversation, got %+v", listBody)
	}
}

func TestAGYMockStreamJobExecution(t *testing.T) {
	mockDir := t.TempDir()
	mockScript := filepath.Join(mockDir, "mock-agy.sh")
	scriptBody := `#!/bin/sh
echo '{"event":"init","conversation_id":"conv-mock-1"}'
echo '{"event":"step_update","step_update":{"step_type":"agent_response","text_delta":"Olá "}}'
echo '{"event":"result","result":{"conversation_id":"conv-mock-1","status":"SUCCESS","response":"Olá mundo"}}'
`
	if err := os.WriteFile(mockScript, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGY_BIN", mockScript)

	mux, _, _ := newTestServer(t)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/jobs", "application/json", strings.NewReader(`{"prompt":"teste agy","model":"gemini-4-hydrogen-high"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created struct {
		JobID string `json:"job_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)

	sseResp, err := http.Get(ts.URL + "/api/jobs/" + created.JobID + "/events?after=0")
	if err != nil {
		t.Fatal(err)
	}
	defer sseResp.Body.Close()
	sseBytes, _ := io.ReadAll(sseResp.Body)
	if !strings.Contains(string(sseBytes), `"conversation_id":"conv-mock-1"`) || !strings.Contains(string(sseBytes), "data: [DONE]") {
		t.Fatalf("unexpected AGY SSE output: %s", string(sseBytes))
	}
}

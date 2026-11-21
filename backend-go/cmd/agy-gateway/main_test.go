package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/db"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/jobs"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/runner"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/wol"
)

func newTestServer(t *testing.T) (*http.ServeMux, *db.GatewayDB, config.GatewaySettings) {
	t.Helper()
	return newTestServerWithEnv(t, nil)
}

func newTestServerWithEnv(t *testing.T, extra map[string]string) (*http.ServeMux, *db.GatewayDB, config.GatewaySettings) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("GATEWAY_MODE", "standalone")
	t.Setenv("ENABLE_WOL", "false")
	t.Setenv("DATA_DIR", tmpDir)
	t.Setenv("CACHE_DIR", filepath.Join(tmpDir, "cache"))
	t.Setenv("INBOX_DIR", filepath.Join(tmpDir, "inbox"))
	t.Setenv("OUTBOX_DIR", filepath.Join(tmpDir, "outbox"))
	t.Setenv("AGY_BRAIN_DIR", filepath.Join(tmpDir, "brain"))
	for k, v := range extra {
		t.Setenv(k, v)
	}

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
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"default_permission_mode":"autonomous","default_model":"auto","theme_mode":"light","theme_palette":"emerald"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/settings expected 200, got %d", rec.Code)
	}
	var updatedSettings map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &updatedSettings)
	if updatedSettings["theme_mode"] != "light" || updatedSettings["theme_palette"] != "emerald" {
		t.Fatalf("expected persisted theme settings, got %+v", updatedSettings)
	}
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"default_permission_mode":"invalid"}`)))
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid permission mode, got %d", badRec.Code)
	}
	badThemeRec := httptest.NewRecorder()
	mux.ServeHTTP(badThemeRec, httptest.NewRequest(http.MethodPut, "/api/settings", bytes.NewBufferString(`{"theme_palette":"neon"}`)))
	if badThemeRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid theme palette, got %d", badThemeRec.Code)
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

func TestIDEBridgeQueueExecution(t *testing.T) {
	bridgeDir := filepath.Join(t.TempDir(), ".agy-bridge")
	mux, _, cfg := newTestServerWithEnv(t, map[string]string{
		"GATEWAY_MODE":           "ide-bridge",
		"IDE_BRIDGE_DIR":         bridgeDir,
		"IDE_BRIDGE_TIMEOUT_SEC": "10",
	})
	if cfg.Mode != "ide-bridge" || cfg.Mods["ide_bridge"] != true {
		t.Fatalf("expected ide-bridge mode enabled, got mode=%s mods=%+v", cfg.Mode, cfg.Mods)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/jobs", "application/json", strings.NewReader(`{"prompt":"oi ide","model":"gemini-4-hydrogen-high"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/jobs status = %d", resp.StatusCode)
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&created)

	inboxPath := filepath.Join(bridgeDir, "inbox", created.JobID+".json")
	outboxPath := filepath.Join(bridgeDir, "outbox", created.JobID+".json")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(inboxPath); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := os.Stat(inboxPath); err != nil {
		t.Fatalf("expected inbox file %s to be written: %v", inboxPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(outboxPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outboxPath, []byte(`{"response":"Resposta da IDE","conversation_id":"ide-conv-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	sseResp, err := http.Get(ts.URL + "/api/jobs/" + created.JobID + "/events?after=0")
	if err != nil {
		t.Fatal(err)
	}
	defer sseResp.Body.Close()
	sseBytes, _ := io.ReadAll(sseResp.Body)
	out := string(sseBytes)
	if !strings.Contains(out, "Resposta da IDE") || !strings.Contains(out, `"conversation_id":"ide-conv-1"`) || !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("unexpected IDE bridge SSE output: %s", out)
	}
}

func TestWhisperTranscribeMod(t *testing.T) {
	// Disabled check first
	disabledMux, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/transcribe", nil)
	rec := httptest.NewRecorder()
	disabledMux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when whisper disabled, got %d", rec.Code)
	}

	whisperSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		_ = file.Close()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"olá whisper"}`))
	}))
	defer whisperSrv.Close()

	mux, _, cfg := newTestServerWithEnv(t, map[string]string{
		"ENABLE_WHISPER": "true",
		"WHISPER_URL":    whisperSrv.URL,
	})
	if cfg.Mods["whisper"] != true {
		t.Fatalf("expected whisper mod true, got %+v", cfg.Mods)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "audio.webm")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("fake-audio-bytes"))
	_ = mw.Close()

	postReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/transcribe", &body)
	if err != nil {
		t.Fatal(err)
	}
	postReq.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(postReq)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/transcribe status = %d: %s", resp.StatusCode, string(raw))
	}
	var payload map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&payload)
	if payload["text"] != "olá whisper" {
		t.Fatalf("unexpected transcribe payload: %+v", payload)
	}
}

func TestConversationMetaSortingFilteringAndSnapshots(t *testing.T) {
	mux, store, _ := newTestServer(t)
	if err := store.EnsureLocalConversation("local-a", "Primeira", "ollama:qwen3:8b"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.EnsureLocalConversation("local-b", "Segunda", "ollama:qwen3:8b"); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Pin local-a, rename it, and attach a project tag
	patchReq, _ := http.NewRequest(
		http.MethodPatch,
		ts.URL+"/api/conversations/local-a/meta",
		strings.NewReader(`{"pinned":true,"custom_title":"Sessão Fixada","tag":"ProjetoX","tag_color":"emerald"}`),
	)
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil || patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH meta failed: err=%v resp=%v", err, patchResp)
	}
	_ = patchResp.Body.Close()

	// Reject invalid tag color
	badReq, _ := http.NewRequest(
		http.MethodPatch,
		ts.URL+"/api/conversations/local-a/meta",
		strings.NewReader(`{"tag_color":"neon"}`),
	)
	badResp, err := http.DefaultClient.Do(badReq)
	if err != nil || badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid tag_color, got %v err=%v", badResp, err)
	}
	_ = badResp.Body.Close()

	// Verify pinned item comes first and engine/tag filters work
	listResp, err := http.Get(ts.URL + "/api/conversations?engine=ollama&tag=ProjetoX")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	var listData struct {
		Conversations []runner.ConversationSummary `json:"conversations"`
		Tags          []string                     `json:"tags"`
	}
	_ = json.NewDecoder(listResp.Body).Decode(&listData)
	if len(listData.Conversations) != 1 || listData.Conversations[0].ID != "local-a" || listData.Conversations[0].Title != "Sessão Fixada" || !listData.Conversations[0].Pinned {
		t.Fatalf("unexpected filtered conversations: %+v", listData)
	}

	// Create snapshot and verify safe escaped HTML rendering
	snapReqBytes, _ := json.Marshal(map[string]string{
		"title":         "Teste <script>",
		"content_md":    "# Titulo\n\n<script>alert(1)</script>\n\n```go\nprintln(\"ok\")\n```",
		"theme_mode":    "light",
		"theme_palette": "violet",
	})
	snapResp, err := http.Post(
		ts.URL+"/api/snapshots",
		"application/json",
		bytes.NewReader(snapReqBytes),
	)
	if err != nil || snapResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/snapshots failed: err=%v resp=%v", err, snapResp)
	}
	defer snapResp.Body.Close()
	var snapBody struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	_ = json.NewDecoder(snapResp.Body).Decode(&snapBody)
	if snapBody.ID == "" || !strings.HasPrefix(snapBody.URL, "/s/") {
		t.Fatalf("unexpected snapshot body: %+v", snapBody)
	}

	pageResp, err := http.Get(ts.URL + snapBody.URL)
	if err != nil || pageResp.StatusCode != http.StatusOK {
		t.Fatalf("GET snapshot page failed: err=%v resp=%v", err, pageResp)
	}
	defer pageResp.Body.Close()
	rawHTML, _ := io.ReadAll(pageResp.Body)
	htmlStr := string(rawHTML)
	if strings.Contains(htmlStr, "<script>alert(1)</script>") || !strings.Contains(htmlStr, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("snapshot did not escape script tag safely: %s", htmlStr)
	}
}

func TestDeleteConversationsLocalAndAGY(t *testing.T) {
	mux, store, _ := newTestServer(t)
	if err := store.EnsureLocalConversation("local-del", "Chat Local Apagar", "ollama:qwen3:8b"); err != nil {
		t.Fatal(err)
	}
	agyBrain := os.Getenv("AGY_BRAIN_DIR")
	logsDir := filepath.Join(agyBrain, "agy-del-1", ".system_generated", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "transcript.jsonl"), []byte("{\"type\":\"USER_INPUT\",\"content\":\"oi\"}\n{\"type\":\"PLANNER_RESPONSE\",\"content\":\"ola\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Delete local conversation
	delLocalReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/conversations/local-del", nil)
	delLocalResp, err := http.DefaultClient.Do(delLocalReq)
	if err != nil || delLocalResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE local failed: err=%v resp=%v", err, delLocalResp)
	}
	var localPayload map[string]any
	_ = json.NewDecoder(delLocalResp.Body).Decode(&localPayload)
	_ = delLocalResp.Body.Close()
	if localPayload["deleted_local"] != true {
		t.Fatalf("expected deleted_local=true, got %+v", localPayload)
	}
	getLocalResp, _ := http.Get(ts.URL + "/api/conversations/local-del")
	_ = getLocalResp.Body.Close()
	if getLocalResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after local delete, got %d", getLocalResp.StatusCode)
	}

	// 2. Delete AGY conversation
	delAGYReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/conversations/agy-del-1", nil)
	delAGYResp, err := http.DefaultClient.Do(delAGYReq)
	if err != nil || delAGYResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE agy failed: err=%v resp=%v", err, delAGYResp)
	}
	var agyPayload map[string]any
	_ = json.NewDecoder(delAGYResp.Body).Decode(&agyPayload)
	_ = delAGYResp.Body.Close()
	if agyPayload["deleted_agy"] != true {
		t.Fatalf("expected deleted_agy=true, got %+v", agyPayload)
	}
	getAGYResp, _ := http.Get(ts.URL + "/api/conversations/agy-del-1")
	_ = getAGYResp.Body.Close()
	if getAGYResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after agy delete, got %d", getAGYResp.StatusCode)
	}

	// 3. Reject encoded path separator / traversal id
	badReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/conversations/..%5Cescape", nil)
	badResp, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid id '..\\\\escape', got %d", badResp.StatusCode)
	}
}

func TestDuplicateUploadDeduplication(t *testing.T) {
	mux, _, cfg := newTestServer(t)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("file", "image.png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte("png-bytes-" + string(rune('0'+i))))
		_ = mw.Close()

		resp, err := http.Post(ts.URL+"/api/upload", mw.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			OK       bool   `json:"ok"`
			Filename string `json:"filename"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&payload)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !payload.OK {
			t.Fatalf("upload %d failed: status=%d payload=%+v", i, resp.StatusCode, payload)
		}
		if seen[payload.Filename] {
			t.Fatalf("duplicate filename returned on upload %d: %s", i, payload.Filename)
		}
		seen[payload.Filename] = true
		if _, err := os.Stat(filepath.Join(cfg.Dirs.InboxDir, payload.Filename)); err != nil {
			t.Fatalf("expected uploaded file %s in inbox: %v", payload.Filename, err)
		}
	}
}

func TestWebhooksAndBotBridgeEndpoints(t *testing.T) {
	// Mock Evolution API to capture WhatsApp reply
	var gotWANumber, gotWAText string
	waCh := make(chan struct{}, 1)
	evoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Number string `json:"number"`
			Text   string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotWANumber = body.Number
		gotWAText = body.Text
		select {
		case waCh <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer evoSrv.Close()

	// Mock Ollama so Auto/Standalone executes deterministically
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3:8b"}]}`))
		case "/api/chat":
			_, _ = w.Write([]byte(`{"message":{"content":"Resposta do bot"},"done":true}` + "\n"))
		}
	}))
	defer ollamaSrv.Close()

	mux, _, _ := newTestServerWithEnv(t, map[string]string{
		"OLLAMA_URL":               ollamaSrv.URL,
		"ENABLE_WEBHOOKS":          "true",
		"WEBHOOK_SECRET":           "siri-secret-123",
		"ENABLE_WHATSAPP_BOT":      "true",
		"EVOLUTION_API_URL":        evoSrv.URL,
		"EVOLUTION_INSTANCE":       "sub-notifier",
		"WHATSAPP_ALLOWED_NUMBERS": "558398677675",
		"ENABLE_DISCORD_BOT":       "true",
		"DISCORD_ALLOWED_USER_IDS": "605488871773437993",
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Webhook rejects missing/wrong token
	unauthResp, _ := http.Post(ts.URL+"/api/webhook/prompt", "application/json", strings.NewReader(`{"prompt":"oi"}`))
	_ = unauthResp.Body.Close()
	if unauthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for missing bearer token, got %d", unauthResp.StatusCode)
	}

	// 2. Webhook wait=true returns reply
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/webhook/prompt", strings.NewReader(`{"prompt":"ping","model":"ollama:qwen3:8b","wait":true}`))
	req.Header.Set("Authorization", "Bearer siri-secret-123")
	req.Header.Set("Content-Type", "application/json")
	okResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var whBody map[string]any
	_ = json.NewDecoder(okResp.Body).Decode(&whBody)
	_ = okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK || whBody["reply"] != "Resposta do bot" {
		t.Fatalf("unexpected webhook wait response: status=%d body=%+v", okResp.StatusCode, whBody)
	}

	// 3. WhatsApp ignores group and accepts allowed DM
	grpResp, _ := http.Post(ts.URL+"/api/bot/whatsapp/webhook", "application/json", strings.NewReader(`{"data":{"key":{"remoteJid":"120363@g.us","fromMe":false},"message":{"conversation":"oi"}}}`))
	var grpBody map[string]any
	_ = json.NewDecoder(grpResp.Body).Decode(&grpBody)
	_ = grpResp.Body.Close()
	if grpBody["ignored"] != true {
		t.Fatalf("expected group whatsapp message to be ignored, got %+v", grpBody)
	}

	waResp, _ := http.Post(ts.URL+"/api/bot/whatsapp/webhook", "application/json", strings.NewReader(`{"data":{"key":{"remoteJid":"558398677675@s.whatsapp.net","fromMe":false},"message":{"conversation":"resuma"}}}`))
	_ = waResp.Body.Close()
	if waResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on allowed whatsapp webhook, got %d", waResp.StatusCode)
	}
	select {
	case <-waCh:
		if gotWANumber != "558398677675" || gotWAText == "" {
			t.Fatalf("unexpected whatsapp reply: number=%q text=%q", gotWANumber, gotWAText)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Evolution API reply")
	}

	// 4. Discord rejects non-DM and accepts allowlisted DM
	dReqBad, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/bot/discord/dm", strings.NewReader(`{"user_id":"605488871773437993","is_dm":false,"prompt":"oi"}`))
	dReqBad.Header.Set("Authorization", "Bearer siri-secret-123")
	dRespBad, _ := http.DefaultClient.Do(dReqBad)
	_ = dRespBad.Body.Close()
	if dRespBad.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for non-DM discord, got %d", dRespBad.StatusCode)
	}

	dReqOk, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/bot/discord/dm", strings.NewReader(`{"user_id":"605488871773437993","is_dm":true,"prompt":"oi","wait":true}`))
	dReqOk.Header.Set("Authorization", "Bearer siri-secret-123")
	dRespOk, _ := http.DefaultClient.Do(dReqOk)
	_ = dRespOk.Body.Close()
	if dRespOk.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for allowlisted discord DM, got %d", dRespOk.StatusCode)
	}
}
func TestCockpitOptionalProxy(t *testing.T) {
	// 1. Default Core keeps cockpit optional package disabled
	muxOff, _, _ := newTestServer(t)
	featRec := httptest.NewRecorder()
	muxOff.ServeHTTP(featRec, httptest.NewRequest(http.MethodGet, "/api/features", nil))
	var featPayload struct {
		Features map[string]any `json:"features"`
	}
	_ = json.Unmarshal(featRec.Body.Bytes(), &featPayload)
	if enabled, _ := featPayload.Features["cockpit"].(bool); enabled {
		t.Fatalf("expected features.cockpit=false by default, got %+v", featPayload.Features)
	}

	// 2. Enabled proxy forwards path & Bearer token
	var gotPath, gotAuth string
	mockCockpit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"hostname":"pc-test","allow_power":true}`)
	}))
	defer mockCockpit.Close()

	muxOn, _, _ := newTestServerWithEnv(t, map[string]string{
		"ENABLE_COCKPIT": "true",
		"COCKPIT_URL":    mockCockpit.URL,
		"COCKPIT_TOKEN":  "bridge-secret",
	})
	rec := httptest.NewRecorder()
	muxOn.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/cockpit/overview", nil))
	if rec.Code != http.StatusOK || gotPath != "/overview" || gotAuth != "Bearer bridge-secret" {
		t.Fatalf("proxy mismatch: status=%d path=%q auth=%q body=%s", rec.Code, gotPath, gotAuth, rec.Body.String())
	}

	// 3. Offline optional backend returns clean 502 JSON
	muxDown, _, _ := newTestServerWithEnv(t, map[string]string{
		"ENABLE_COCKPIT": "true",
		"COCKPIT_URL":    "http://127.0.0.1:1",
	})
	downRec := httptest.NewRecorder()
	muxDown.ServeHTTP(downRec, httptest.NewRequest(http.MethodGet, "/api/cockpit/overview", nil))
	if downRec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when cockpit unreachable, got %d", downRec.Code)
	}
}

func TestAutomationsCronAlertsAndWebPushRoutes(t *testing.T) {
	mux, _, _ := newTestServer(t)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// 1. Service Worker, Manifest, Apple Touch Icon & VAPID key
	swResp, err := http.Get(ts.URL + "/sw.js")
	if err != nil || swResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sw.js failed: err=%v resp=%v", err, swResp)
	}
	_ = swResp.Body.Close()
	if swResp.Header.Get("Service-Worker-Allowed") != "/" {
		t.Fatalf("expected Service-Worker-Allowed=/ header on /sw.js, got %q", swResp.Header.Get("Service-Worker-Allowed"))
	}

	manifestResp, err := http.Get(ts.URL + "/manifest.json")
	if err != nil || manifestResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /manifest.json failed: err=%v resp=%v", err, manifestResp)
	}
	manifestBytes, _ := io.ReadAll(manifestResp.Body)
	_ = manifestResp.Body.Close()
	if !strings.Contains(string(manifestBytes), `"standalone"`) || !strings.Contains(string(manifestBytes), `"maskable"`) {
		t.Fatalf("unexpected manifest content: %s", string(manifestBytes))
	}

	appleIconResp, err := http.Get(ts.URL + "/static/apple-touch-icon.png")
	if err != nil || appleIconResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /static/apple-touch-icon.png failed: err=%v resp=%v", err, appleIconResp)
	}
	_ = appleIconResp.Body.Close()

	vapidResp, err := http.Get(ts.URL + "/api/push/vapid-public-key")
	if err != nil || vapidResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/push/vapid-public-key failed: err=%v resp=%v", err, vapidResp)
	}
	var vapidBody struct {
		PublicKey string `json:"public_key"`
		Supported bool   `json:"supported"`
	}
	_ = json.NewDecoder(vapidResp.Body).Decode(&vapidBody)
	_ = vapidResp.Body.Close()
	if !vapidBody.Supported || len(vapidBody.PublicKey) < 40 {
		t.Fatalf("unexpected VAPID response: %+v", vapidBody)
	}

	// 2. Push subscribe & unsubscribe
	subBody := `{"endpoint":"https://push.example/sub1","keys":{"p256dh":"abc","auth":"def"}}`
	subResp, _ := http.Post(ts.URL+"/api/push/subscribe", "application/json", strings.NewReader(subBody))
	_ = subResp.Body.Close()
	if subResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/push/subscribe got %d", subResp.StatusCode)
	}
	unsubResp, _ := http.Post(ts.URL+"/api/push/unsubscribe", "application/json", strings.NewReader(`{"endpoint":"https://push.example/sub1"}`))
	_ = unsubResp.Body.Close()
	if unsubResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/push/unsubscribe got %d", unsubResp.StatusCode)
	}

	// 3. Cron automation validation & CRUD + run now
	badCronResp, _ := http.Post(ts.URL+"/api/automations", "application/json", strings.NewReader(`{"name":"Bad","cron_expr":"99 * * * *","kind":"check"}`))
	_ = badCronResp.Body.Close()
	if badCronResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 on invalid cron, got %d", badCronResp.StatusCode)
	}

	okCronResp, err := http.Post(
		ts.URL+"/api/automations",
		"application/json",
		strings.NewReader(`{"name":"Checar 08h","cron_expr":"0 8 * * *","kind":"check","prompt":"disco e docker","notify_on":"always"}`),
	)
	if err != nil || okCronResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/automations failed: err=%v resp=%v", err, okCronResp)
	}
	var created struct {
		ID        string `json:"id"`
		NextRunAt string `json:"next_run_at"`
	}
	_ = json.NewDecoder(okCronResp.Body).Decode(&created)
	_ = okCronResp.Body.Close()
	if created.ID == "" || created.NextRunAt == "" {
		t.Fatalf("expected ID and NextRunAt, got %+v", created)
	}

	patchReq, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/automations/"+created.ID, strings.NewReader(`{"enabled":false}`))
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, _ := http.DefaultClient.Do(patchReq)
	_ = patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH /api/automations got %d", patchResp.StatusCode)
	}

	runResp, _ := http.Post(ts.URL+"/api/automations/"+created.ID+"/run", "application/json", nil)
	_ = runResp.Body.Close()
	if runResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/automations/{id}/run got %d", runResp.StatusCode)
	}
	time.Sleep(50 * time.Millisecond)

	// 4. Alert config, test dispatch, list, read, clear
	cfgReq, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/alerts/config", strings.NewReader(`{"notify_job_done":true,"notify_long_job_sec":30,"notify_docker_down":true,"gpu_temp_threshold_c":80,"disk_free_min_pct":12,"ntfy_topic":"agy-test","web_push_enabled":true,"notify_desktop":false}`))
	cfgReq.Header.Set("Content-Type", "application/json")
	cfgResp, _ := http.DefaultClient.Do(cfgReq)
	_ = cfgResp.Body.Close()
	if cfgResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/alerts/config got %d", cfgResp.StatusCode)
	}

	testAlertResp, _ := http.Post(ts.URL+"/api/alerts/test", "application/json", nil)
	_ = testAlertResp.Body.Close()
	if testAlertResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/alerts/test got %d", testAlertResp.StatusCode)
	}

	listAlertsResp, _ := http.Get(ts.URL + "/api/alerts?limit=10")
	var alertListBody struct {
		Alerts      []db.AlertEvent `json:"alerts"`
		UnreadCount int             `json:"unread_count"`
	}
	_ = json.NewDecoder(listAlertsResp.Body).Decode(&alertListBody)
	_ = listAlertsResp.Body.Close()
	if len(alertListBody.Alerts) == 0 || alertListBody.UnreadCount == 0 {
		t.Fatalf("expected recorded alerts, got %+v", alertListBody)
	}

	readResp, _ := http.Post(ts.URL+"/api/alerts/read", "application/json", strings.NewReader(`{}`))
	_ = readResp.Body.Close()
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/alerts/read got %d", readResp.StatusCode)
	}

	delAutoReq, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/automations/"+created.ID, nil)
	delAutoResp, _ := http.DefaultClient.Do(delAutoReq)
	_ = delAutoResp.Body.Close()
	if delAutoResp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/automations got %d", delAutoResp.StatusCode)
	}
}

func TestCustomSlashCommandsAndOutboxInlinePreview(t *testing.T) {
	mux, store, cfg := newTestServer(t)

	// 1. Seeded slash commands + create/validate/update/delete/reset
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, httptest.NewRequest(http.MethodGet, "/api/slash-commands", nil))
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), "/publish") {
		t.Fatalf("GET /api/slash-commands unexpected response: code=%d body=%s", listRec.Code, listRec.Body.String())
	}

	// Reserved built-in blocked
	badBuiltinRec := httptest.NewRecorder()
	mux.ServeHTTP(badBuiltinRec, httptest.NewRequest(http.MethodPost, "/api/slash-commands", strings.NewReader(`{"command":"/diff","prompt_template":"nope"}`)))
	if badBuiltinRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for reserved built-in /diff, got %d", badBuiltinRec.Code)
	}

	// Create custom command (normalizes missing slash & uppercase)
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/api/slash-commands", strings.NewReader(`{
		"command":"Logs_Api",
		"description":"Analisar logs recentes",
		"prompt_template":"Inspect {{input}} logs",
		"model":"gemini-4-hydrogen-high",
		"auto_submit":false
	}`)))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("POST /api/slash-commands expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var createRes struct {
		Command db.SlashCommand `json:"command"`
	}
	_ = json.Unmarshal(createRec.Body.Bytes(), &createRes)
	if createRes.Command.Command != "/logs_api" || createRes.Command.AutoSubmit {
		t.Fatalf("unexpected normalized slash command: %+v", createRes.Command)
	}

	// Duplicate command conflict
	dupRec := httptest.NewRecorder()
	mux.ServeHTTP(dupRec, httptest.NewRequest(http.MethodPost, "/api/slash-commands", strings.NewReader(`{"command":"/logs_api","prompt_template":"dup"}`)))
	if dupRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate slash command, got %d", dupRec.Code)
	}

	// Partial PUT update
	putRec := httptest.NewRecorder()
	mux.ServeHTTP(putRec, httptest.NewRequest(http.MethodPut, "/api/slash-commands/"+createRes.Command.ID, strings.NewReader(`{"description":"Logs de produção"}`)))
	if putRec.Code != http.StatusOK || !strings.Contains(putRec.Body.String(), "Logs de produção") {
		t.Fatalf("PUT /api/slash-commands expected 200 updated description, got %d: %s", putRec.Code, putRec.Body.String())
	}

	// Delete custom command
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, httptest.NewRequest(http.MethodDelete, "/api/slash-commands/"+createRes.Command.ID, nil))
	if delRec.Code != http.StatusOK {
		t.Fatalf("DELETE /api/slash-commands got %d", delRec.Code)
	}

	// Reset defaults returns seeded commands cleanly
	resetRec := httptest.NewRecorder()
	mux.ServeHTTP(resetRec, httptest.NewRequest(http.MethodPost, "/api/slash-commands/reset", nil))
	if resetRec.Code != http.StatusOK || !strings.Contains(resetRec.Body.String(), "/fin") {
		t.Fatalf("POST /api/slash-commands/reset got %d: %s", resetRec.Code, resetRec.Body.String())
	}

	// 2. Outbox inline image / PDF serving vs attachment download
	if err := os.WriteFile(filepath.Join(cfg.Dirs.OutboxDir, "preview.png"), []byte("\x89PNG\r\n\x1a\nfakepng"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dirs.OutboxDir, "doc.pdf"), []byte("%PDF-1.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Dirs.OutboxDir, "icon.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), 0o644); err != nil {
		t.Fatal(err)
	}

	imgInlineRec := httptest.NewRecorder()
	mux.ServeHTTP(imgInlineRec, httptest.NewRequest(http.MethodGet, "/api/outbox/preview.png?inline=1", nil))
	if imgInlineRec.Code != http.StatusOK ||
		imgInlineRec.Header().Get("Content-Type") != "image/png" ||
		!strings.HasPrefix(imgInlineRec.Header().Get("Content-Disposition"), "inline;") {
		t.Fatalf("unexpected inline png headers: code=%d ct=%q cd=%q", imgInlineRec.Code, imgInlineRec.Header().Get("Content-Type"), imgInlineRec.Header().Get("Content-Disposition"))
	}

	pdfInlineRec := httptest.NewRecorder()
	mux.ServeHTTP(pdfInlineRec, httptest.NewRequest(http.MethodGet, "/api/outbox/doc.pdf?inline=1", nil))
	if pdfInlineRec.Code != http.StatusOK ||
		pdfInlineRec.Header().Get("Content-Type") != "application/pdf" ||
		!strings.HasPrefix(pdfInlineRec.Header().Get("Content-Disposition"), "inline;") {
		t.Fatalf("unexpected inline pdf headers: code=%d ct=%q cd=%q", pdfInlineRec.Code, pdfInlineRec.Header().Get("Content-Type"), pdfInlineRec.Header().Get("Content-Disposition"))
	}

	svgInlineRec := httptest.NewRecorder()
	mux.ServeHTTP(svgInlineRec, httptest.NewRequest(http.MethodGet, "/api/outbox/icon.svg?inline=1", nil))
	if svgInlineRec.Header().Get("Content-Security-Policy") != "default-src 'none'" {
		t.Fatalf("expected strict CSP on inline svg, got %q", svgInlineRec.Header().Get("Content-Security-Policy"))
	}

	dlRec := httptest.NewRecorder()
	mux.ServeHTTP(dlRec, httptest.NewRequest(http.MethodGet, "/api/outbox/preview.png", nil))
	if !strings.HasPrefix(dlRec.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("expected attachment disposition without ?inline=1, got %q", dlRec.Header().Get("Content-Disposition"))
	}

	// 3. Reloading conversation attaches recorded Outbox artifacts onto matching assistant message
	if err := store.EnsureLocalConversation("local-art-1", "Gráficos", "ollama:ryan-fast:8b"); err != nil {
		t.Fatal(err)
	}
	_ = store.AppendLocalMessage("local-art-1", "user", "Gere o gráfico e o PDF")
	_ = store.AppendLocalMessage("local-art-1", "assistant", "Aqui estão os arquivos: preview.png e doc.pdf")
	if err := store.RecordConversationArtifacts("local-art-1", "job-123", "Aqui estão os arquivos: preview.png", []string{"preview.png", "doc.pdf"}); err != nil {
		t.Fatal(err)
	}

	convRec := httptest.NewRecorder()
	mux.ServeHTTP(convRec, httptest.NewRequest(http.MethodGet, "/api/conversations/local-art-1", nil))
	if convRec.Code != http.StatusOK {
		t.Fatalf("GET /api/conversations/local-art-1 got %d", convRec.Code)
	}
	var convBody struct {
		Messages []struct {
			Role        string   `json:"role"`
			OutboxFiles []string `json:"outbox_files"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(convRec.Body.Bytes(), &convBody)
	if len(convBody.Messages) != 2 || len(convBody.Messages[1].OutboxFiles) != 2 {
		t.Fatalf("expected outbox_files attached on assistant message, got %+v", convBody.Messages)
	}

	// 4. Local gateway single-instance health detector
	muxTS := httptest.NewServer(mux)
	defer muxTS.Close()
	parsedURL, _ := url.Parse(muxTS.URL)
	if !isGatewayHealthyLocal(parsedURL.Port()) {
		t.Fatalf("expected isGatewayHealthyLocal(%s) = true", parsedURL.Port())
	}
	if isGatewayHealthyLocal("1") {
		t.Fatalf("expected isGatewayHealthyLocal(1) = false")
	}
}

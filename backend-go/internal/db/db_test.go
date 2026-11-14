package db

import (
	"path/filepath"
	"testing"
)

func TestDBSettingsJobsAndFTS5(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// 1. Default settings
	s, err := store.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	if s["default_permission_mode"] != "copilot" || s["default_model"] != "gemini-4-hydrogen-high" {
		t.Fatalf("unexpected defaults: %+v", s)
	}

	// 2. Update settings & verify persistence across reopen
	updated, err := store.UpdateSettings(map[string]any{
		"default_permission_mode": "readonly",
		"default_model":           "auto",
	})
	if err != nil {
		t.Fatalf("UpdateSettings failed: %v", err)
	}
	if updated["default_permission_mode"] != "readonly" || updated["default_model"] != "auto" {
		t.Fatalf("unexpected updated settings: %+v", updated)
	}

	// 3. Create running job + append SSE events + verify orphaned job cleanup on reopen
	if _, err := store.CreateJob("job-1", nil, "agy", "auto", "copilot", "hello"); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}
	seq1, err := store.AppendJobEvent("job-1", "delta", map[string]any{"type": "delta", "text": "hi"})
	if err != nil || seq1 != 1 {
		t.Fatalf("AppendJobEvent seq1=%d err=%v", seq1, err)
	}
	seq2, err := store.AppendJobEvent("job-1", "done", map[string]any{"type": "done"})
	if err != nil || seq2 != 2 {
		t.Fatalf("AppendJobEvent seq2=%d err=%v", seq2, err)
	}
	events, err := store.GetJobEventsAfter("job-1", 1)
	if err != nil || len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("GetJobEventsAfter failed: len=%d err=%v", len(events), err)
	}

	// 4. Local conversation + FTS5 prefix search
	if err := store.EnsureLocalConversation("local-1", "Docker Compose", "ollama:ryan-fast:8b"); err != nil {
		t.Fatalf("EnsureLocalConversation failed: %v", err)
	}
	if err := store.AppendLocalMessage("local-1", "user", "Como configurar rede bridge no Raspberry?"); err != nil {
		t.Fatalf("AppendLocalMessage failed: %v", err)
	}
	ids, err := store.SearchFTS("Raspb", 10)
	if err != nil || len(ids) != 1 || ids[0] != "local-1" {
		t.Fatalf("SearchFTS failed: ids=%v err=%v", ids, err)
	}

	_ = store.Close()

	// Reopen and verify settings persisted + orphaned job transitioned to error
	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer reopened.Close()

	s2, err := reopened.GetSettings()
	if err != nil || s2["default_permission_mode"] != "readonly" {
		t.Fatalf("settings did not persist: %+v err=%v", s2, err)
	}
	j, err := reopened.GetJob("job-1")
	if err != nil || j == nil || j.Status != "error" {
		t.Fatalf("expected orphaned job status=error, got %+v err=%v", j, err)
	}
}

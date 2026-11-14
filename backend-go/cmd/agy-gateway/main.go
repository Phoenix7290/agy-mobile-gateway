package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/db"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/jobs"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/runner"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/web"
)

const appVersion = "3.3.0-go-m3"

var defaultAGYModels = []runner.ModelInfo{
	{ID: "gemini-4-hydrogen-high", Name: "Gemini 4 Hydrogen (High)", Type: "cloud", Badge: "Hydrogen"},
	{ID: "gemini-4-hydrogen-xhigh", Name: "Gemini 4 Hydrogen (Extra)", Type: "cloud", Badge: "Extra"},
	{ID: "gemini-4-hydrogen-medium", Name: "Gemini 4 Hydrogen (Medium)", Type: "cloud", Badge: "Hydrogen"},
	{ID: "gemini-3.8-flash-high", Name: "Gemini 3.8 Flash (High)", Type: "cloud", Badge: "Rápido"},
	{ID: "gemini-4-argon-high", Name: "Gemini 4 Argon (High)", Type: "cloud", Badge: "Raciocínio"},
	{ID: "claude-sonnet-5-5-high", Name: "Claude Sonnet 5.5 (High)", Type: "cloud", Badge: "Engenharia"},
	{ID: "claude-opus-5-5-high", Name: "Claude Opus 5.5 (High)", Type: "cloud", Badge: "Top"},
	{ID: "gpt-oss-120b-medium", Name: "GPT-OSS 120B (Medium)", Type: "cloud", Badge: "Open"},
}

type settingsUpdateRequest struct {
	DefaultPermissionMode *string `json:"default_permission_mode"`
	DefaultModel          *string `json:"default_model"`
	DefaultEffort         *string `json:"default_effort"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func activeJobIDForConversation(store *db.GatewayDB, convID string) *string {
	active, err := store.GetActiveJobs()
	if err != nil {
		return nil
	}
	for _, j := range active {
		if j.ConversationID != nil && *j.ConversationID == convID {
			id := j.ID
			return &id
		}
	}
	return nil
}

func buildMux(cfg config.GatewaySettings, store *db.GatewayDB, r runner.Runner, mgr *jobs.Manager) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"runtime": "go",
			"version": appVersion,
		})
	})

	mux.HandleFunc("GET /api/features", func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"version": appVersion,
			"mode":    cfg.Mode,
			"dirs": map[string]string{
				"inbox":  cfg.Dirs.InboxDir,
				"outbox": cfg.Dirs.OutboxDir,
			},
			"features": cfg.Mods,
		})
	})

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, req *http.Request) {
		var wolMAC any
		if enabled, _ := cfg.Mods["wol"].(bool); enabled && cfg.WolMAC != "" {
			wolMAC = cfg.WolMAC
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"online":      r.CheckOnline(req.Context(), 1500*time.Millisecond),
			"mode":        cfg.Mode,
			"target_host": cfg.TargetHost,
			"wol_mac":     wolMAC,
			"target_user": cfg.TargetUser,
		})
	})

	mux.HandleFunc("GET /api/models", func(w http.ResponseWriter, req *http.Request) {
		models := []runner.ModelInfo{}
		if enabled, _ := cfg.Mods["ollama"].(bool); enabled {
			models = append(models, runner.ModelInfo{
				ID:    "auto",
				Name:  "Auto (Híbrido: Local + Cloud)",
				Type:  "cloud",
				Badge: "Economia",
			})
		}
		models = append(models, defaultAGYModels...)
		if enabled, _ := cfg.Mods["ollama"].(bool); enabled && r.CheckOnline(req.Context(), 1*time.Second) {
			models = append(models, r.ListOllamaModels(req.Context())...)
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": models})
	})

	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, req *http.Request) {
		settings, err := store.GetSettings()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, settings)
	})

	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, req *http.Request) {
		var body settingsUpdateRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "JSON inválido"})
			return
		}
		patch := map[string]any{}
		if body.DefaultPermissionMode != nil {
			if _, ok := jobs.PermissionModes[*body.DefaultPermissionMode]; !ok {
				writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Modo de permissão inválido"})
				return
			}
			patch["default_permission_mode"] = *body.DefaultPermissionMode
		}
		if body.DefaultModel != nil {
			patch["default_model"] = *body.DefaultModel
		}
		if body.DefaultEffort != nil {
			patch["default_effort"] = *body.DefaultEffort
		}
		updated, err := store.UpdateSettings(patch)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, updated)
	})

	mux.HandleFunc("GET /api/conversations", func(w http.ResponseWriter, req *http.Request) {
		q := strings.TrimSpace(req.URL.Query().Get("q"))
		limit := parseIntQuery(req.URL.Query().Get("limit"), 50, 1, 200)
		offset := parseIntQuery(req.URL.Query().Get("offset"), 0, 0, 100000)

		merged := []runner.ConversationSummary{}
		if r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			agyConvs, _ := r.ListAGYConversations(req.Context(), 100)
			for _, c := range agyConvs {
				c.Engine = "agy"
				merged = append(merged, c)
				_ = store.UpsertFTS(c.ID, c.Title, c.Title)
			}
		}

		localConvs, _ := store.ListLocalConversations()
		for _, lc := range localConvs {
			merged = append(merged, runner.ConversationSummary{
				ID:           lc.ID,
				Title:        lc.Title,
				UpdatedAt:    lc.UpdatedAt,
				MessageCount: lc.MessageCount,
				Model:        lc.Model,
				Engine:       "ollama",
			})
		}

		sort.Slice(merged, func(i, j int) bool {
			return merged[i].UpdatedAt > merged[j].UpdatedAt
		})

		if q != "" {
			qLow := strings.ToLower(q)
			ftsList, _ := store.SearchFTS(qLow, 100)
			ftsSet := map[string]bool{}
			for _, id := range ftsList {
				ftsSet[id] = true
			}
			filtered := []runner.ConversationSummary{}
			for _, c := range merged {
				if ftsSet[c.ID] ||
					strings.Contains(strings.ToLower(c.Title), qLow) ||
					strings.Contains(strings.ToLower(c.ID), qLow) {
					filtered = append(filtered, c)
				}
			}
			merged = filtered
		}

		total := len(merged)
		end := offset + limit
		if offset > total {
			offset = total
		}
		if end > total {
			end = total
		}
		page := merged[offset:end]
		writeJSON(w, http.StatusOK, map[string]any{
			"conversations": page,
			"total":         total,
			"has_more":      end < total,
		})
	})

	mux.HandleFunc("GET /api/conversations/{conv_id}", func(w http.ResponseWriter, req *http.Request) {
		convID := req.PathValue("conv_id")
		if localConv, err := store.GetLocalConversation(convID); err == nil && localConv != nil {
			msgs := make([]runner.ConversationMessage, 0, len(localConv.Messages))
			for _, m := range localConv.Messages {
				msgs = append(msgs, runner.ConversationMessage{Role: m.Role, Content: m.Content})
			}
			writeJSON(w, http.StatusOK, runner.ConversationDetail{
				ID:          localConv.ID,
				Title:       localConv.Title,
				UpdatedAt:   localConv.UpdatedAt,
				Model:       localConv.Model,
				Engine:      "ollama",
				ActiveJobID: activeJobIDForConversation(store, convID),
				Messages:    msgs,
			})
			return
		}

		if r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			if detail, err := r.GetAGYConversation(req.Context(), convID); err == nil && detail != nil {
				detail.Engine = "agy"
				detail.ActiveJobID = activeJobIDForConversation(store, convID)
				writeJSON(w, http.StatusOK, detail)
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Conversa não encontrada"})
	})

	mux.HandleFunc("DELETE /api/conversations/{conv_id}", func(w http.ResponseWriter, req *http.Request) {
		deleted, _ := store.DeleteLocalConversation(req.PathValue("conv_id"))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted_local": deleted})
	})

	mux.HandleFunc("POST /api/jobs", func(w http.ResponseWriter, req *http.Request) {
		var body jobs.CreateRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "JSON inválido"})
			return
		}
		if strings.TrimSpace(body.Prompt) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Prompt não pode ser vazio"})
			return
		}
		if !r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"detail": fmt.Sprintf("Host (%s) está inacessível.", cfg.TargetHost),
			})
			return
		}
		res, err := mgr.StartJob(req.Context(), body)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})

	mux.HandleFunc("POST /api/jobs/{job_id}/cancel", func(w http.ResponseWriter, req *http.Request) {
		status := mgr.CancelJob(req.PathValue("job_id"))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": status})
	})

	mux.HandleFunc("GET /api/jobs/active", func(w http.ResponseWriter, req *http.Request) {
		active, err := store.GetActiveJobs()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		if active == nil {
			active = []db.Job{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": active})
	})

	mux.HandleFunc("GET /api/jobs/{job_id}", func(w http.ResponseWriter, req *http.Request) {
		job, err := store.GetJob(req.PathValue("job_id"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		if job == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Job não encontrado"})
			return
		}
		writeJSON(w, http.StatusOK, job)
	})

	mux.HandleFunc("GET /api/jobs/{job_id}/events", func(w http.ResponseWriter, req *http.Request) {
		jobID := req.PathValue("job_id")
		job, err := store.GetJob(jobID)
		if err != nil || job == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Job não encontrado"})
			return
		}

		cursor := parseIntQuery(req.URL.Query().Get("after"), 0, 0, 1000000)
		if lastID := strings.TrimSpace(req.Header.Get("Last-Event-ID")); lastID != "" {
			if parsed, convErr := strconv.Atoi(lastID); convErr == nil && parsed > cursor {
				cursor = parsed
			}
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": "Streaming não suportado"})
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		for {
			events, _ := store.GetJobEventsAfter(jobID, cursor)
			for _, ev := range events {
				cursor = ev.Seq
				raw, _ := json.Marshal(ev.Payload)
				_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", cursor, raw)
			}
			flusher.Flush()

			currentJob, _ := store.GetJob(jobID)
			if currentJob == nil || currentJob.Status != "running" {
				tailEvents, _ := store.GetJobEventsAfter(jobID, cursor)
				for _, ev := range tailEvents {
					cursor = ev.Seq
					raw, _ := json.Marshal(ev.Payload)
					_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", cursor, raw)
				}
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
				flusher.Flush()
				return
			}

			sig := mgr.WaitSignal(jobID)
			timer := time.NewTimer(8 * time.Second)
			select {
			case <-req.Context().Done():
				timer.Stop()
				return
			case <-sig:
				timer.Stop()
			case <-timer.C:
				_, _ = fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	})

	web.RegisterRoutes(mux, web.StaticFS())
	return mux
}

func parseIntQuery(raw string, defVal, minVal, maxVal int) int {
	if raw == "" {
		return defVal
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return defVal
	}
	if v < minVal {
		return minVal
	}
	if v > maxVal {
		return maxVal
	}
	return v
}

func main() {
	cfg := config.Load()

	// Ensure OS-resolved directories exist on startup, mirroring Python main.py.
	for _, dir := range []string{cfg.Dirs.DataDir, cfg.Dirs.CacheDir, cfg.Dirs.InboxDir, cfg.Dirs.OutboxDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("warning: could not create dir %s: %v", dir, err)
		}
	}

	store, err := db.Open(filepath.Join(cfg.Dirs.DataDir, "gateway.db"))
	if err != nil {
		log.Fatalf("failed to open sqlite db: %v", err)
	}
	defer store.Close()

	execRunner := runner.Create(cfg)
	jobMgr := jobs.NewManager(cfg, store, execRunner)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           buildMux(cfg, store, execRunner, jobMgr),
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("AGY Mobile Gateway (Go %s) listening on http://0.0.0.0:%s [mode=%s]", appVersion, cfg.Port, cfg.Mode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("AGY Mobile Gateway stopped cleanly")
}

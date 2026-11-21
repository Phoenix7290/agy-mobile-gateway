package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/automation"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/botbridge"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/db"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/jobs"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/runner"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/web"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/wol"
	"github.com/google/uuid"
)

const appVersion = "3.3.0-go"

var safeFilenameRe = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

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

var validThemeModes = map[string]bool{
	"dark":  true,
	"light": true,
}

var validThemePalettes = map[string]bool{
	"sky":     true,
	"emerald": true,
	"violet":  true,
	"indigo":  true,
	"amber":   true,
	"rose":    true,
}

type snapshotCreateRequest struct {
	Title        string `json:"title"`
	ContentMD    string `json:"content_md"`
	ThemeMode    string `json:"theme_mode"`
	ThemePalette string `json:"theme_palette"`
}

func clipRunes(s string, maxLen int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxLen {
		return string(r)
	}
	return string(r[:maxLen])
}

type settingsUpdateRequest struct {
	DefaultPermissionMode *string `json:"default_permission_mode"`
	DefaultModel          *string `json:"default_model"`
	DefaultEffort         *string `json:"default_effort"`
	ThemeMode             *string `json:"theme_mode"`
	ThemePalette          *string `json:"theme_palette"`
}

type wolRequest struct {
	MAC *string `json:"mac"`
}

type watchdogState struct {
	mu             sync.RWMutex
	Online         *bool   `json:"online"`
	LastChecked    *string `json:"last_checked"`
	LastSeenOnline *string `json:"last_seen_online"`
	UptimeChecks   int     `json:"uptime_checks"`
	DowntimeEvents int     `json:"downtime_events"`
}

func (w *watchdogState) probe(ctx context.Context, r runner.Runner) {
	online := r.CheckOnline(ctx, 1500*time.Millisecond)
	nowISO := time.Now().UTC().Format(time.RFC3339)

	w.mu.Lock()
	defer w.mu.Unlock()
	prev := w.Online
	w.Online = &online
	w.LastChecked = &nowISO
	w.UptimeChecks++
	if online {
		w.LastSeenOnline = &nowISO
	} else if prev != nil && *prev {
		w.DowntimeEvents++
	}
}

func (w *watchdogState) snapshot() map[string]any {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return map[string]any{
		"online":           w.Online,
		"last_checked":     w.LastChecked,
		"last_seen_online": w.LastSeenOnline,
		"uptime_checks":    w.UptimeChecks,
		"downtime_events":  w.DowntimeEvents,
	}
}

func sanitizeFilename(name string) string {
	base := filepath.Base(name)
	return safeFilenameRe.ReplaceAllString(base, "_")
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

func newTerminalProxy(ttydWSURL string) http.Handler {
	parsed, err := url.Parse(ttydWSURL)
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "TTYD_WS_URL inválida", http.StatusBadGateway)
		})
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	}
	targetPath := parsed.Path
	if targetPath == "" {
		targetPath = "/terminal/ws"
	}
	baseURL := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(baseURL)
			pr.Out.URL.Path = targetPath
		},
	}
}

func newCockpitProxy(cockpitURL, token string) http.Handler {
	parsed, err := url.Parse(strings.TrimRight(cockpitURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"detail": "COCKPIT_URL inválida"})
		})
	}
	baseURL := &url.URL{Scheme: parsed.Scheme, Host: parsed.Host}
	basePath := strings.TrimRight(parsed.Path, "/")
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(baseURL)
			subPath := strings.TrimPrefix(pr.In.URL.Path, "/api/cockpit")
			if subPath == "" {
				subPath = "/"
			}
			pr.Out.URL.Path = basePath + subPath
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			if token != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+token)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"detail": "Cockpit optional package indisponível"})
		},
	}
}

func buildMux(cfg config.GatewaySettings, store *db.GatewayDB, r runner.Runner, mgr *jobs.Manager, wd *watchdogState) *http.ServeMux {
	notifier := automation.NewNotifier(store)
	mgr.OnJobFinish = notifier.HandleJobFinish
	sched := automation.NewScheduler(cfg, store, r, mgr, notifier)
	return buildAppMux(cfg, store, r, mgr, wd, notifier, sched)
}

func buildAppMux(
	cfg config.GatewaySettings,
	store *db.GatewayDB,
	r runner.Runner,
	mgr *jobs.Manager,
	wd *watchdogState,
	notifier *automation.Notifier,
	sched *automation.Scheduler,
) *http.ServeMux {
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
		online := r.CheckOnline(req.Context(), 1500*time.Millisecond)
		activeOS := r.ActiveOS(req.Context())
		unreadAlerts, _ := store.CountUnreadAlerts()
		writeJSON(w, http.StatusOK, map[string]any{
			"online":        online,
			"mode":          cfg.Mode,
			"active_os":     activeOS,
			"target_host":   cfg.TargetHost,
			"wol_mac":       wolMAC,
			"target_user":   cfg.TargetUser,
			"unread_alerts": unreadAlerts,
		})
	})

	mux.HandleFunc("GET /api/watchdog", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, wd.snapshot())
	})

	mux.HandleFunc("GET /api/telemetry", func(w http.ResponseWriter, req *http.Request) {
		if enabled, _ := cfg.Mods["telemetry"].(bool); !enabled {
			writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
			return
		}
		data := r.GetTelemetry(req.Context())
		data.Enabled = true
		writeJSON(w, http.StatusOK, data)
	})

	mux.HandleFunc("POST /api/wol", func(w http.ResponseWriter, req *http.Request) {
		if enabled, _ := cfg.Mods["wol"].(bool); !enabled {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Wake-on-LAN não habilitado neste perfil"})
			return
		}
		var body wolRequest
		if req.Body != nil {
			_ = json.NewDecoder(req.Body).Decode(&body)
		}
		macToWake := cfg.WolMAC
		if body.MAC != nil && strings.TrimSpace(*body.MAC) != "" {
			macToWake = strings.TrimSpace(*body.MAC)
		}
		if err := wol.SendMagicPacket(macToWake); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": fmt.Sprintf("Erro ao enviar WoL: %v", err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": fmt.Sprintf("Magic Packet enviado para %s", macToWake)})
	})

	mux.Handle("/terminal/ws", newTerminalProxy(cfg.TtydWSURL))

	var (
		agyModelsMu        sync.Mutex
		cachedAGYModels    []runner.ModelInfo
		cachedAGYModelsExp time.Time
	)

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

		agyModelsMu.Lock()
		agyList := cachedAGYModels
		expired := len(agyList) == 0 || time.Now().After(cachedAGYModelsExp)
		agyModelsMu.Unlock()

		if expired && r.CheckOnline(req.Context(), 1*time.Second) {
			if live := r.ListAGYModels(req.Context()); len(live) > 0 {
				agyList = live
				agyModelsMu.Lock()
				cachedAGYModels = live
				cachedAGYModelsExp = time.Now().Add(5 * time.Minute)
				agyModelsMu.Unlock()
			}
		}
		if len(agyList) == 0 {
			agyList = defaultAGYModels
		}
		models = append(models, agyList...)

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
		if body.ThemeMode != nil {
			if !validThemeModes[*body.ThemeMode] {
				writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Modo de tema inválido"})
				return
			}
			patch["theme_mode"] = *body.ThemeMode
		}
		if body.ThemePalette != nil {
			if !validThemePalettes[*body.ThemePalette] {
				writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Paleta de cor inválida"})
				return
			}
			patch["theme_palette"] = *body.ThemePalette
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
		engineFilter := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("engine")))
		tagFilter := strings.ToLower(strings.TrimSpace(req.URL.Query().Get("tag")))
		limit := parseIntQuery(req.URL.Query().Get("limit"), 50, 1, 200)
		offset := parseIntQuery(req.URL.Query().Get("offset"), 0, 0, 100000)

		metaMap, _ := store.ListConversationMeta()

		merged := []runner.ConversationSummary{}
		if r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			agyConvs, _ := r.ListAGYConversations(req.Context(), 100)
			for _, c := range agyConvs {
				c.Engine = "agy"
				merged = append(merged, c)
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

		tagSet := map[string]string{}
		for i := range merged {
			if m, ok := metaMap[merged[i].ID]; ok {
				merged[i].Pinned = m.Pinned
				merged[i].Tag = m.Tag
				merged[i].TagColor = m.TagColor
				merged[i].CustomTitle = m.CustomTitle
				if m.CustomTitle != "" {
					merged[i].Title = m.CustomTitle
				}
				if m.Tag != "" {
					tagSet[strings.ToLower(m.Tag)] = m.Tag
				}
			}
			if merged[i].Engine == "agy" {
				ftsBody := strings.TrimSpace(merged[i].Title + " " + merged[i].Tag)
				_ = store.UpsertFTS(merged[i].ID, merged[i].Title, ftsBody)
			}
		}

		tags := make([]string, 0, len(tagSet))
		for _, t := range tagSet {
			tags = append(tags, t)
		}
		sort.Strings(tags)

		sort.Slice(merged, func(i, j int) bool {
			if merged[i].Pinned != merged[j].Pinned {
				return merged[i].Pinned
			}
			return merged[i].UpdatedAt > merged[j].UpdatedAt
		})

		if engineFilter == "agy" || engineFilter == "ollama" {
			filtered := []runner.ConversationSummary{}
			for _, c := range merged {
				if c.Engine == engineFilter {
					filtered = append(filtered, c)
				}
			}
			merged = filtered
		}

		if tagFilter != "" {
			filtered := []runner.ConversationSummary{}
			for _, c := range merged {
				if strings.ToLower(c.Tag) == tagFilter {
					filtered = append(filtered, c)
				}
			}
			merged = filtered
		}

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
					strings.Contains(strings.ToLower(c.Tag), qLow) ||
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
			"tags":          tags,
		})
	})

	mux.HandleFunc("GET /api/conversations/{conv_id}", func(w http.ResponseWriter, req *http.Request) {
		convID := req.PathValue("conv_id")
		meta, _ := store.GetConversationMeta(convID)

		if localConv, err := store.GetLocalConversation(convID); err == nil && localConv != nil {
			msgs := make([]runner.ConversationMessage, 0, len(localConv.Messages))
			for _, m := range localConv.Messages {
				msgs = append(msgs, runner.ConversationMessage{Role: m.Role, Content: m.Content})
			}
			detail := runner.ConversationDetail{
				ID:          localConv.ID,
				Title:       localConv.Title,
				UpdatedAt:   localConv.UpdatedAt,
				Model:       localConv.Model,
				Engine:      "ollama",
				ActiveJobID: activeJobIDForConversation(store, convID),
				Messages:    msgs,
			}
			attachArtifactsToMessages(store, convID, detail.Messages)
			if meta != nil {
				detail.Pinned = meta.Pinned
				detail.Tag = meta.Tag
				detail.TagColor = meta.TagColor
				if meta.CustomTitle != "" {
					detail.Title = meta.CustomTitle
				}
			}
			writeJSON(w, http.StatusOK, detail)
			return
		}

		if r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			if detail, err := r.GetAGYConversation(req.Context(), convID); err == nil && detail != nil {
				detail.Engine = "agy"
				detail.ActiveJobID = activeJobIDForConversation(store, convID)
				attachArtifactsToMessages(store, convID, detail.Messages)
				if meta != nil {
					detail.Pinned = meta.Pinned
					detail.Tag = meta.Tag
					detail.TagColor = meta.TagColor
					if meta.CustomTitle != "" {
						detail.Title = meta.CustomTitle
					}
				}
				writeJSON(w, http.StatusOK, detail)
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Conversa não encontrada"})
	})

	mux.HandleFunc("PATCH /api/conversations/{conv_id}/meta", func(w http.ResponseWriter, req *http.Request) {
		convID := req.PathValue("conv_id")
		var body db.ConversationMetaPatch
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "JSON inválido"})
			return
		}
		if body.TagColor != nil && *body.TagColor != "" && !validThemePalettes[*body.TagColor] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Cor de etiqueta inválida"})
			return
		}
		if body.CustomTitle != nil {
			t := clipRunes(*body.CustomTitle, 120)
			body.CustomTitle = &t
		}
		if body.Tag != nil {
			t := clipRunes(*body.Tag, 32)
			body.Tag = &t
		}
		updated, err := store.UpsertConversationMeta(convID, body)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		if updated.CustomTitle != "" || updated.Tag != "" {
			_ = store.UpsertFTS(convID, updated.CustomTitle, strings.TrimSpace(updated.CustomTitle+" "+updated.Tag))
		}
		writeJSON(w, http.StatusOK, updated)
	})

	mux.HandleFunc("DELETE /api/conversations/{conv_id}", func(w http.ResponseWriter, req *http.Request) {
		convID := strings.TrimSpace(req.PathValue("conv_id"))
		if convID == "" || convID == "." || convID == ".." || strings.ContainsAny(convID, `/\`) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "ID de conversa inválido"})
			return
		}
		if active, err := store.GetActiveJobs(); err == nil {
			for _, j := range active {
				if j.ConversationID != nil && *j.ConversationID == convID {
					_ = mgr.CancelJob(j.ID)
				}
			}
		}
		deletedLocal, err := store.DeleteLocalConversation(convID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		deletedAGY := false
		if !deletedLocal && !strings.HasPrefix(convID, "local-") && r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			d, err := r.DeleteAGYConversation(req.Context(), convID)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"detail": err.Error()})
				return
			}
			deletedAGY = d
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":            true,
			"deleted_local": deletedLocal,
			"deleted_agy":   deletedAGY,
		})
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

	// --- Inbox Upload & Outbox Files ---

	var (
		uploadMu  sync.Mutex
		uploadSeq uint64
	)

	mux.HandleFunc("POST /api/upload", func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseMultipartForm(64 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Multipart inválido"})
			return
		}
		file, header, err := req.FormFile("file")
		if err != nil || header == nil || strings.TrimSpace(header.Filename) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Nome de arquivo vazio"})
			return
		}
		defer file.Close()

		cleanName := sanitizeFilename(header.Filename)
		uploadDir := filepath.Join(cfg.Dirs.CacheDir, "uploads")
		if err := os.MkdirAll(uploadDir, 0o755); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}

		uploadMu.Lock()
		uploadSeq++
		seq := uploadSeq
		targetName := cleanName
		ext := filepath.Ext(cleanName)
		stem := strings.TrimSuffix(cleanName, ext)
		_, statErr := os.Stat(filepath.Join(uploadDir, targetName))
		if statErr == nil || strings.EqualFold(stem, "image") {
			targetName = fmt.Sprintf("%s_%s_%d%s", stem, time.Now().Format("20060102-150405"), seq, ext)
		}
		localPath := filepath.Join(uploadDir, targetName)
		out, err := os.Create(localPath)
		uploadMu.Unlock()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		written, copyErr := io.Copy(out, file)
		_ = out.Close()
		if copyErr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": copyErr.Error()})
			return
		}

		destPath, err := r.SaveToInbox(req.Context(), localPath, targetName)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": fmt.Sprintf("Erro ao salvar arquivo no inbox: %v", err)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":       true,
			"filename": targetName,
			"path":     destPath,
			"size":     written,
		})
	})

	mux.HandleFunc("GET /api/outbox", func(w http.ResponseWriter, req *http.Request) {
		if !r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			writeJSON(w, http.StatusOK, map[string]any{"files": []runner.OutboxFile{}})
			return
		}
		files, err := r.ListOutbox(req.Context())
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"files": []runner.OutboxFile{}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": files})
	})

	mux.HandleFunc("GET /api/outbox/{filename}", func(w http.ResponseWriter, req *http.Request) {
		cleanName := sanitizeFilename(req.PathValue("filename"))
		if !r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Host offline"})
			return
		}
		rc, closeFn, err := r.OpenOutboxFile(req.Context(), cleanName)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"detail": "Arquivo não encontrado"})
			return
		}
		defer func() { _ = closeFn() }()

		ext := strings.ToLower(filepath.Ext(cleanName))
		inlineContentTypes := map[string]string{
			".png":  "image/png",
			".jpg":  "image/jpeg",
			".jpeg": "image/jpeg",
			".webp": "image/webp",
			".gif":  "image/gif",
			".svg":  "image/svg+xml",
			".pdf":  "application/pdf",
		}
		contentType, isInlineMedia := inlineContentTypes[ext]
		if !isInlineMedia {
			contentType = mime.TypeByExtension(ext)
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		inlineParam := strings.TrimSpace(req.URL.Query().Get("inline"))
		wantsInline := (inlineParam == "1" || strings.EqualFold(inlineParam, "true")) && isInlineMedia

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if wantsInline {
			if ext == ".svg" {
				w.Header().Set("Content-Security-Policy", "default-src 'none'")
			}
			w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, cleanName))
		} else {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, cleanName))
		}
		_, _ = io.Copy(w, rc)
	})

	mux.HandleFunc("DELETE /api/outbox/{filename}", func(w http.ResponseWriter, req *http.Request) {
		cleanName := sanitizeFilename(req.PathValue("filename"))
		_ = r.DeleteOutboxFile(req.Context(), cleanName)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// Optional faster-whisper audio transcription endpoint (OpenAI-compatible /v1/audio/transcriptions).
	mux.HandleFunc("POST /api/transcribe", func(w http.ResponseWriter, req *http.Request) {
		if enabled, _ := cfg.Mods["whisper"].(bool); !enabled || cfg.WhisperURL == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Mod Whisper não habilitado (configure ENABLE_WHISPER=true e WHISPER_URL)"})
			return
		}
		if err := req.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Áudio multipart inválido"})
			return
		}
		file, header, err := req.FormFile("file")
		if err != nil || header == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Arquivo de áudio ausente"})
			return
		}
		defer file.Close()

		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("file", sanitizeFilename(header.Filename))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		if _, err := io.Copy(fw, file); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		_ = mw.WriteField("model", "Systran/faster-whisper-small")
		_ = mw.Close()

		endpoint := strings.TrimRight(cfg.WhisperURL, "/") + "/v1/audio/transcriptions"
		upReq, err := http.NewRequestWithContext(req.Context(), http.MethodPost, endpoint, &body)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		upReq.Header.Set("Content-Type", mw.FormDataContentType())

		client := &http.Client{Timeout: 30 * time.Second}
		upResp, err := client.Do(upReq)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"detail": fmt.Sprintf("Erro ao contatar Whisper: %v", err)})
			return
		}
		defer upResp.Body.Close()

		var parsed struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(upResp.Body).Decode(&parsed)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "text": strings.TrimSpace(parsed.Text)})
	})

	// Read-only Shareable Snapshots
	mux.HandleFunc("POST /api/snapshots", func(w http.ResponseWriter, req *http.Request) {
		var body snapshotCreateRequest
		if err := json.NewDecoder(io.LimitReader(req.Body, 2<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "JSON inválido"})
			return
		}
		if strings.TrimSpace(body.ContentMD) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Conteúdo vazio"})
			return
		}
		mode := "dark"
		if body.ThemeMode == "light" {
			mode = "light"
		}
		palette := "sky"
		if validThemePalettes[body.ThemePalette] {
			palette = body.ThemePalette
		}
		title := clipRunes(body.Title, 120)
		if title == "" {
			title = "Snapshot AGY"
		}
		id := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
		snap, err := store.CreateSnapshot(id, title, body.ContentMD, mode, palette)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":         snap.ID,
			"url":        "/s/" + snap.ID,
			"created_at": snap.CreatedAt,
		})
	})

	mux.HandleFunc("GET /s/{snapshot_id}", func(w http.ResponseWriter, req *http.Request) {
		snapID := req.PathValue("snapshot_id")
		snap, err := store.GetSnapshot(snapID)
		if err != nil || snap == nil {
			http.NotFound(w, req)
			return
		}
		htmlOut := web.RenderSnapshotHTML(snap.Title, snap.ContentMD, snap.CreatedAt, snap.ThemeMode, snap.ThemePalette)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, htmlOut)
	})

	bridge := botbridge.New(cfg, store, mgr)

	// --- Optional Package: Webhooks (Siri / Tasker / Home Assistant) ---
	mux.HandleFunc("POST /api/webhook/prompt", func(w http.ResponseWriter, req *http.Request) {
		if enabled, _ := cfg.Mods["webhooks"].(bool); !enabled {
			writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Webhooks desativados (ENABLE_WEBHOOKS=false)"})
			return
		}
		if !botbridge.VerifyBearer(req.Header.Get("Authorization"), cfg.WebhookSecret) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Token Bearer inválido"})
			return
		}
		var body struct {
			Prompt         string  `json:"prompt"`
			Model          string  `json:"model"`
			PermissionMode string  `json:"permission_mode"`
			ConversationID *string `json:"conversation_id"`
			Wait           bool    `json:"wait"`
			TimeoutSec     int     `json:"timeout_sec"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "JSON inválido"})
			return
		}
		if strings.TrimSpace(body.Prompt) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Prompt não pode ser vazio"})
			return
		}
		if !r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"detail": "Host offline"})
			return
		}
		if body.Model == "" {
			body.Model = "auto"
		}
		if body.PermissionMode == "" {
			body.PermissionMode = "copilot"
		}
		started, err := mgr.StartJob(req.Context(), jobs.CreateRequest{
			Prompt:         body.Prompt,
			Model:          &body.Model,
			PermissionMode: &body.PermissionMode,
			ConversationID: body.ConversationID,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		jobID, _ := started["job_id"].(string)
		if !body.Wait {
			writeJSON(w, http.StatusOK, started)
			return
		}
		timeoutSec := body.TimeoutSec
		if timeoutSec <= 0 {
			timeoutSec = 20
		}
		if timeoutSec > 55 {
			timeoutSec = 55
		}
		res, waitErr := bridge.WaitForJobResult(req.Context(), jobID, time.Duration(timeoutSec)*time.Second)
		if waitErr != nil {
			writeJSON(w, http.StatusAccepted, map[string]any{
				"ok":      true,
				"job_id":  jobID,
				"status":  "running",
				"pending": true,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":              res.Status == "completed" || res.Status == "done",
			"job_id":          res.JobID,
			"conversation_id": res.ConversationID,
			"status":          res.Status,
			"reply":           res.Reply,
			"outbox_files":    res.OutboxFiles,
			"error":           res.Error,
		})
	})

	// --- Optional Package: WhatsApp Bot Bridge (Evolution API v2, DM Allowlist) ---
	mux.HandleFunc("POST /api/bot/whatsapp/webhook", func(w http.ResponseWriter, req *http.Request) {
		if enabled, _ := cfg.Mods["whatsapp_bot"].(bool); !enabled {
			writeJSON(w, http.StatusForbidden, map[string]string{"detail": "WhatsApp bot desativado"})
			return
		}
		if cfg.WhatsAppWebhookSecret != "" {
			got := req.Header.Get("X-Webhook-Secret")
			if got == "" {
				got = req.URL.Query().Get("secret")
			}
			if got != cfg.WhatsAppWebhookSecret {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Webhook secret inválido"})
				return
			}
		}
		var payload struct {
			Event string `json:"event"`
			Data  struct {
				Key struct {
					RemoteJID string `json:"remoteJid"`
					FromMe    bool   `json:"fromMe"`
				} `json:"key"`
				Message struct {
					Conversation        string `json:"conversation"`
					ExtendedTextMessage struct {
						Text string `json:"text"`
					} `json:"extendedTextMessage"`
				} `json:"message"`
			} `json:"data"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "JSON inválido"})
			return
		}
		number, allowed := bridge.IsAllowedWhatsApp(payload.Data.Key.RemoteJID, payload.Data.Key.FromMe)
		if !allowed {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
			return
		}
		text := strings.TrimSpace(payload.Data.Message.Conversation)
		if text == "" {
			text = strings.TrimSpace(payload.Data.Message.ExtendedTextMessage.Text)
		}
		if text == "" {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ignored": true})
			return
		}
		if !r.CheckOnline(req.Context(), 1500*time.Millisecond) {
			_ = bridge.SendWhatsAppText(req.Context(), number, "Host offline no momento.")
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "offline": true})
			return
		}
		autoModel, copilotPerm := "auto", "copilot"
		started, err := mgr.StartJob(req.Context(), jobs.CreateRequest{
			Prompt:         text,
			Model:          &autoModel,
			PermissionMode: &copilotPerm,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		jobID, _ := started["job_id"].(string)
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			res, err := bridge.WaitForJobResult(bgCtx, jobID, 3*time.Minute)
			if err != nil {
				_ = bridge.SendWhatsAppText(bgCtx, number, fmt.Sprintf("Erro ao aguardar resposta: %v", err))
				return
			}
			reply := res.Reply
			if res.Status == "error" && res.Error != "" {
				reply = "Erro: " + res.Error
			}
			if len(res.OutboxFiles) > 0 {
				reply += "\n\nOutbox: " + strings.Join(res.OutboxFiles, ", ")
			}
			if strings.TrimSpace(reply) != "" {
				_ = bridge.SendWhatsAppText(bgCtx, number, reply)
			}
		}()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job_id": jobID})
	})

	// --- Optional Package: Discord DM Bridge (Nudge Allowlist) ---
	mux.HandleFunc("POST /api/bot/discord/dm", func(w http.ResponseWriter, req *http.Request) {
		if enabled, _ := cfg.Mods["discord_bot"].(bool); !enabled {
			writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Discord bot desativado"})
			return
		}
		if cfg.WebhookSecret != "" && !botbridge.VerifyBearer(req.Header.Get("Authorization"), cfg.WebhookSecret) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"detail": "Token Bearer inválido"})
			return
		}
		var body struct {
			UserID string `json:"user_id"`
			IsDM   bool   `json:"is_dm"`
			Prompt string `json:"prompt"`
			Wait   bool   `json:"wait"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "JSON inválido"})
			return
		}
		if !bridge.IsAllowedDiscordUser(body.UserID, body.IsDM) {
			writeJSON(w, http.StatusForbidden, map[string]string{"detail": "Usuário ou canal não autorizado (DM allowlist)"})
			return
		}
		if strings.TrimSpace(body.Prompt) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Prompt vazio"})
			return
		}
		autoModel, copilotPerm := "auto", "copilot"
		started, err := mgr.StartJob(req.Context(), jobs.CreateRequest{
			Prompt:         body.Prompt,
			Model:          &autoModel,
			PermissionMode: &copilotPerm,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		jobID, _ := started["job_id"].(string)
		if body.Wait {
			res, _ := bridge.WaitForJobResult(req.Context(), jobID, 25*time.Second)
			writeJSON(w, http.StatusOK, res)
			return
		}
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			res, err := bridge.WaitForJobResult(bgCtx, jobID, 3*time.Minute)
			if err == nil && strings.TrimSpace(res.Reply) != "" {
				_ = bridge.SendDiscordDM(bgCtx, body.UserID, res.Reply)
			}
		}()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "job_id": jobID})
	})

	if enabled, _ := cfg.Mods["cockpit"].(bool); enabled {
		mux.Handle("/api/cockpit/", newCockpitProxy(cfg.CockpitURL, cfg.CockpitToken))
	}
	registerAutomationRoutes(mux, store, notifier, sched)
	registerSlashCommandRoutes(mux, store)

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

func isGatewayHealthyLocal(port string) bool {
	client := &http.Client{Timeout: 450 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var payload struct {
		OK      bool   `json:"ok"`
		Runtime string `json:"runtime"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&payload) != nil {
		return false
	}
	return payload.OK && payload.Runtime == "go"
}

func main() {
	if maybeRunConsoleController() {
		return
	}

	cfg := config.Load()

	shouldOpenBrowser := slices.Contains(os.Args[1:], "--open")
	withSidecars := slices.Contains(os.Args[1:], "--with-sidecars")
	if runtime.GOOS == "windows" && !slices.Contains(os.Args[1:], "--daemon") && cfg.TLSCertFile == "" && strings.TrimSpace(os.Getenv("NO_SINGLE_INSTANCE_OPEN")) == "" {
		if isGatewayHealthyLocal(cfg.Port) {
			log.Printf("AGY Mobile Gateway already running on http://127.0.0.1:%s; opening UI...", cfg.Port)
			openGatewayAppWindow(cfg.Port)
			return
		}
	}

	var sidecarSup *consoleSupervisor
	if withSidecars {
		sidecarSup = newConsoleSupervisor(cfg)
		sidecarSup.mu.Lock()
		sidecarSup.startSidecarsLocked()
		sidecarSup.mu.Unlock()
		defer sidecarSup.stopOwnedChildrenOnly()
	}

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
	wd := &watchdogState{}
	notifier := automation.NewNotifier(store)
	jobMgr.OnJobFinish = notifier.HandleJobFinish
	sched := automation.NewScheduler(cfg, store, execRunner, jobMgr, notifier)

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	sched.Start(bgCtx)
	if enabled, _ := cfg.Mods["watchdog"].(bool); enabled {
		go func() {
			wd.probe(bgCtx, execRunner)
			ticker := time.NewTicker(60 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-bgCtx.Done():
					return
				case <-ticker.C:
					wd.probe(bgCtx, execRunner)
				}
			}
		}()
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           buildAppMux(cfg, store, execRunner, jobMgr, wd, notifier, sched),
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		if cfg.TLSCertFile != "" && cfg.TLSKeyFile != "" {
			log.Printf("AGY Mobile Gateway (Go %s) listening on https://0.0.0.0:%s [mode=%s]", appVersion, cfg.Port, cfg.Mode)
			if err := srv.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("server tls error: %v", err)
			}
			return
		}
		log.Printf("AGY Mobile Gateway (Go %s) listening on http://0.0.0.0:%s [mode=%s]", appVersion, cfg.Port, cfg.Mode)
		if shouldOpenBrowser {
			go func() {
				time.Sleep(350 * time.Millisecond)
				openGatewayAppWindow(cfg.Port)
			}()
		}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-stop
	bgCancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Println("AGY Mobile Gateway stopped cleanly")
}

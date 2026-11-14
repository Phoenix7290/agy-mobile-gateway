package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/db"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/web"
)

const appVersion = "3.3.0-go-m2"

var validPermissionModes = map[string]bool{
	"readonly":   true,
	"copilot":    true,
	"autonomous": true,
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

func checkTargetOnline(cfg config.GatewaySettings, timeout time.Duration) bool {
	if cfg.Mode != "gateway" {
		// In standalone mode, AGY executes directly on the local host.
		return true
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.TargetHost, "22"), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func buildMux(cfg config.GatewaySettings, store *db.GatewayDB) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"runtime": "go",
			"version": appVersion,
		})
	})

	mux.HandleFunc("GET /api/features", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		var wolMAC any
		if enabled, _ := cfg.Mods["wol"].(bool); enabled && cfg.WolMAC != "" {
			wolMAC = cfg.WolMAC
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"online":      checkTargetOnline(cfg, 1500*time.Millisecond),
			"mode":        cfg.Mode,
			"target_host": cfg.TargetHost,
			"wol_mac":     wolMAC,
			"target_user": cfg.TargetUser,
		})
	})

	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		settings, err := store.GetSettings()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, settings)
	})

	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var req settingsUpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "JSON inválido"})
			return
		}
		patch := map[string]any{}
		if req.DefaultPermissionMode != nil {
			if !validPermissionModes[*req.DefaultPermissionMode] {
				writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Modo de permissão inválido"})
				return
			}
			patch["default_permission_mode"] = *req.DefaultPermissionMode
		}
		if req.DefaultModel != nil {
			patch["default_model"] = *req.DefaultModel
		}
		if req.DefaultEffort != nil {
			patch["default_effort"] = *req.DefaultEffort
		}

		updated, err := store.UpdateSettings(patch)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, updated)
	})

	web.RegisterRoutes(mux, web.StaticFS())
	return mux
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

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           buildMux(cfg, store),
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

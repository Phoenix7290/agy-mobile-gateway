package config

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// AppDirs holds resolved OS-appropriate storage paths without polluting user home root.
type AppDirs struct {
	DataDir   string
	CacheDir  string
	InboxDir  string
	OutboxDir string
}

// GatewaySettings mirrors Python GatewaySettings 1:1 for API and mod parity.
type GatewaySettings struct {
	Mode       string
	Port       string
	TargetHost string
	TargetUser string
	WolMAC     string
	SSHKey     string
	OllamaURL  string
	TtydWSURL  string
	Dirs       AppDirs
	Mods       map[string]any
}

func envOr(name, fallback string) string {
	if val, ok := os.LookupEnv(name); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return fallback
}

func boolEnv(name string, fallback bool) bool {
	val, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(val) == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// loadDotEnv reads .env entries only if not already set in process environment.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		val := strings.Trim(strings.TrimSpace(v), `"'`)
		if key != "" {
			if _, exists := os.LookupEnv(key); !exists {
				_ = os.Setenv(key, val)
			}
		}
	}
}

// GetAppDirs resolves Data, Cache, Inbox, and Outbox directories per OS with env overrides.
func GetAppDirs() AppDirs {
	inDocker := fileExists("/.dockerenv")
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	var defData, defCache, defInbox, defOutbox string

	switch {
	case inDocker:
		defData = "/data"
		dataDir := envOr("DATA_DIR", defData)
		return AppDirs{
			DataDir:   dataDir,
			CacheDir:  envOr("CACHE_DIR", filepath.Join(dataDir, "cache")),
			InboxDir:  envOr("INBOX_DIR", "/home/ai/agy-inbox"),
			OutboxDir: envOr("OUTBOX_DIR", "/home/ai/agy-outbox"),
		}
	case runtime.GOOS == "windows":
		localApp := envOr("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		defData = filepath.Join(localApp, "agy-gateway", "data")
		defCache = filepath.Join(localApp, "agy-gateway", "cache")
		docsBase := filepath.Join(home, "Documents", "AGY-Gateway")
		defInbox = filepath.Join(docsBase, "inbox")
		defOutbox = filepath.Join(docsBase, "outbox")
	case runtime.GOOS == "darwin":
		appSupport := filepath.Join(home, "Library", "Application Support", "agy-gateway")
		defData = filepath.Join(appSupport, "data")
		defCache = filepath.Join(home, "Library", "Caches", "agy-gateway")
		defInbox = filepath.Join(home, "agy-inbox")
		defOutbox = filepath.Join(home, "agy-outbox")
	default:
		xdgData := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		xdgCache := envOr("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		defData = filepath.Join(xdgData, "agy-gateway")
		defCache = filepath.Join(xdgCache, "agy-gateway")
		defInbox = filepath.Join(home, "agy-inbox")
		defOutbox = filepath.Join(home, "agy-outbox")
	}

	return AppDirs{
		DataDir:   envOr("DATA_DIR", defData),
		CacheDir:  envOr("CACHE_DIR", defCache),
		InboxDir:  envOr("INBOX_DIR", defInbox),
		OutboxDir: envOr("OUTBOX_DIR", defOutbox),
	}
}

// Load reads .env (local or parent) and returns normalized GatewaySettings.
func Load() GatewaySettings {
	loadDotEnv(".env")
	loadDotEnv("../.env")

	inDocker := fileExists("/.dockerenv")
	envMode := strings.ToLower(envOr("GATEWAY_MODE", ""))
	mode := "standalone"
	if envMode == "gateway" || envMode == "standalone" {
		mode = envMode
	} else if inDocker || envOr("TARGET_HOST", "") != "" {
		mode = "gateway"
	}

	isGateway := mode == "gateway"
	dirs := GetAppDirs()

	defaultMAC := ""
	defaultHost := "127.0.0.1"
	defaultUser := envOr("USER", envOr("USERNAME", "local"))
	if isGateway {
		defaultMAC = "bc:fc:e7:ca:84:05"
		defaultHost = "192.168.1.190"
		defaultUser = "ai"
	}

	wolMAC := envOr("WOL_MAC", defaultMAC)

	mods := map[string]any{
		"terminal":     boolEnv("ENABLE_TERMINAL", isGateway),
		"wol":          boolEnv("ENABLE_WOL", isGateway && wolMAC != ""),
		"watchdog":     boolEnv("ENABLE_WATCHDOG", isGateway),
		"ollama":       boolEnv("ENABLE_OLLAMA", true),
		"inbox_outbox": true,
		"fts5_search":  true,
		"ai_memory":    boolEnv("ENABLE_AI_MEMORY", false) || envOr("AI_MEMORY_SERVER_URL", "") != "",
		"ntfy":         boolEnv("ENABLE_NTFY", false) || envOr("NTFY_TOPIC", "") != "",
		"whisper":      boolEnv("ENABLE_WHISPER", false),
		"telemetry":    boolEnv("ENABLE_TELEMETRY", isGateway),
		"slash_commands": []string{
			"/compact", "/usage", "/diff", "/mem", "/plan", "/clear", "/status",
		},
	}

	return GatewaySettings{
		Mode:       mode,
		Port:       envOr("PORT", "8088"),
		TargetHost: envOr("TARGET_HOST", defaultHost),
		TargetUser: envOr("TARGET_USER", defaultUser),
		WolMAC:     wolMAC,
		SSHKey:     envOr("SSH_KEY", "/root/.ssh/id_ed25519"),
		OllamaURL:  envOr("OLLAMA_URL", "http://127.0.0.1:11434"),
		TtydWSURL:  envOr("TTYD_WS_URL", "ws://127.0.0.1:7681/terminal/ws"),
		Dirs:       dirs,
		Mods:       mods,
	}
}

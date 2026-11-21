package config

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	Mode                   string
	Port                   string
	TargetHost             string
	TargetUser             string
	TargetSSHPort          string
	WinTargetHost          string
	WinTargetUser          string
	WinSSHPort             string
	WinAGYBin              string
	WinInboxDir            string
	WinOutboxDir           string
	WolMAC                 string
	SSHKey                 string
	OllamaURL              string
	TtydWSURL              string
	WhisperURL             string
	CockpitURL             string
	CockpitToken           string
	IDEBridgeDir           string
	IDEBridgeTimeoutSec    int
	TLSCertFile            string
	TLSKeyFile             string
	WebhookSecret          string
	EvolutionAPIURL        string
	EvolutionInstance      string
	EvolutionAPIKey        string
	WhatsAppAllowedNumbers []string
	WhatsAppWebhookSecret  string
	DiscordBotToken        string
	DiscordAllowedUserIDs  []string
	RetentionDays          int
	BrainBackupMaxMB       int
	Dirs                   AppDirs
	Mods                   map[string]any
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

func intEnv(name string, fallback int) int {
	val, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(val) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
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
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
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

// DefaultConfigDir returns the OS-standard directory for persistent gateway configuration (.env).
func DefaultConfigDir() string {
	if fileExists("/.dockerenv") {
		return "/data"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	switch runtime.GOOS {
	case "windows":
		localApp := envOr("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		return filepath.Join(localApp, "agy-gateway")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "agy-gateway")
	default:
		xdgConfig := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		return filepath.Join(xdgConfig, "agy-gateway")
	}
}

// Load reads .env (local, parent, executable directory, config dir, or DataDir) and returns normalized GatewaySettings.
func Load() GatewaySettings {
	loadDotEnv(".env")
	loadDotEnv("../.env")
	if _, hasCustomDataDir := os.LookupEnv("DATA_DIR"); !hasCustomDataDir {
		if exePath, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exePath)
			if !strings.Contains(strings.ToLower(exeDir), "go-build") {
				loadDotEnv(filepath.Join(exeDir, ".env"))
			}
		}
		loadDotEnv(filepath.Join(DefaultConfigDir(), ".env"))
		loadDotEnv(filepath.Join(GetAppDirs().DataDir, ".env"))
	} else {
		loadDotEnv(filepath.Join(GetAppDirs().DataDir, ".env"))
	}

	inDocker := fileExists("/.dockerenv")
	envMode := strings.ToLower(envOr("GATEWAY_MODE", ""))
	mode := "standalone"
	if envMode == "gateway" || envMode == "standalone" || envMode == "ide-bridge" {
		mode = envMode
	} else if inDocker || envOr("TARGET_HOST", "") != "" {
		mode = "gateway"
	}

	isGateway := mode == "gateway"
	isIDEBridge := mode == "ide-bridge" || boolEnv("ENABLE_IDE_BRIDGE", false)
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
	whisperURL := envOr("WHISPER_URL", "")
	cockpitURL := envOr("COCKPIT_URL", "")
	cockpitToken := strings.TrimSpace(os.Getenv("COCKPIT_TOKEN"))
	bridgeDir := envOr("IDE_BRIDGE_DIR", filepath.Join(dirs.DataDir, "agy-bridge"))
	tlsCertFile := envOr("TLS_CERT_FILE", "")
	tlsKeyFile := envOr("TLS_KEY_FILE", "")
	bridgeTimeout := intEnv("IDE_BRIDGE_TIMEOUT_SEC", 180)
	if bridgeTimeout <= 0 {
		bridgeTimeout = 180
	}
	retentionDays := intEnv("RETENTION_DAYS", 30)
	brainBackupMaxMB := intEnv("BRAIN_BACKUP_MAX_MB", 512)

	cockpitEnabled := boolEnv("ENABLE_COCKPIT", false) || cockpitURL != ""
	if cockpitURL == "" {
		cockpitURL = "http://127.0.0.1:8099"
	}

	targetHost := envOr("TARGET_HOST", defaultHost)
	targetUser := envOr("TARGET_USER", defaultUser)
	targetSSHPort := envOr("TARGET_SSH_PORT", "22")
	winTargetUser := envOr("WIN_TARGET_USER", "")
	winTargetHost := envOr("WIN_TARGET_HOST", "")
	if winTargetUser != "" && winTargetHost == "" {
		winTargetHost = targetHost
	}
	winSSHPort := envOr("WIN_SSH_PORT", "22")
	winAGYBin := envOr("WIN_AGY_BIN", "agy")
	winInboxDir := envOr("WIN_INBOX_DIR", "")
	winOutboxDir := envOr("WIN_OUTBOX_DIR", "")
	dualBootEnabled := isGateway && winTargetUser != "" && winTargetHost != ""

	mods := map[string]any{
		"terminal":     boolEnv("ENABLE_TERMINAL", isGateway),
		"wol":          boolEnv("ENABLE_WOL", isGateway && wolMAC != ""),
		"watchdog":     boolEnv("ENABLE_WATCHDOG", isGateway),
		"dual_boot":    dualBootEnabled,
		"ollama":       boolEnv("ENABLE_OLLAMA", true),
		"inbox_outbox": true,
		"fts5_search":  true,
		"ai_memory":    boolEnv("ENABLE_AI_MEMORY", false) || envOr("AI_MEMORY_SERVER_URL", "") != "",
		"ntfy":         boolEnv("ENABLE_NTFY", false) || envOr("NTFY_TOPIC", "") != "",
		"whisper":      boolEnv("ENABLE_WHISPER", false) || whisperURL != "",
		"cockpit":      cockpitEnabled,
		"automations":  true,
		"web_push":     true,
		"ide_bridge":   isIDEBridge,
		"telemetry":    boolEnv("ENABLE_TELEMETRY", isGateway),
		"webhooks":     boolEnv("ENABLE_WEBHOOKS", false),
		"whatsapp_bot": boolEnv("ENABLE_WHATSAPP_BOT", false),
		"discord_bot":  boolEnv("ENABLE_DISCORD_BOT", false),
		"telegram_bot": boolEnv("ENABLE_TELEGRAM_BOT", false),
		"slash_commands": []string{
			"/compact", "/usage", "/diff", "/mem", "/plan", "/clear", "/status",
		},
	}

	return GatewaySettings{
		Mode:                   mode,
		Port:                   envOr("PORT", "8088"),
		TargetHost:             targetHost,
		TargetUser:             targetUser,
		TargetSSHPort:          targetSSHPort,
		WinTargetHost:          winTargetHost,
		WinTargetUser:          winTargetUser,
		WinSSHPort:             winSSHPort,
		WinAGYBin:              winAGYBin,
		WinInboxDir:            winInboxDir,
		WinOutboxDir:           winOutboxDir,
		WolMAC:                 wolMAC,
		SSHKey:                 envOr("SSH_KEY", "/root/.ssh/id_ed25519"),
		OllamaURL:              envOr("OLLAMA_URL", "http://127.0.0.1:11434"),
		TtydWSURL:              envOr("TTYD_WS_URL", "ws://127.0.0.1:7681/terminal/ws"),
		WhisperURL:             whisperURL,
		CockpitURL:             cockpitURL,
		CockpitToken:           cockpitToken,
		IDEBridgeDir:           bridgeDir,
		IDEBridgeTimeoutSec:    bridgeTimeout,
		TLSCertFile:            tlsCertFile,
		TLSKeyFile:             tlsKeyFile,
		WebhookSecret:          envOr("WEBHOOK_SECRET", ""),
		EvolutionAPIURL:        strings.TrimRight(envOr("EVOLUTION_API_URL", "http://127.0.0.1:8085"), "/"),
		EvolutionInstance:      envOr("EVOLUTION_INSTANCE", "sub-notifier"),
		EvolutionAPIKey:        envOr("EVOLUTION_API_KEY", ""),
		WhatsAppAllowedNumbers: splitCSV(envOr("WHATSAPP_ALLOWED_NUMBERS", "")),
		WhatsAppWebhookSecret:  envOr("WHATSAPP_WEBHOOK_SECRET", ""),
		DiscordBotToken:        envOr("DISCORD_BOT_TOKEN", ""),
		DiscordAllowedUserIDs:  splitCSV(envOr("DISCORD_ALLOWED_USER_IDS", "")),
		RetentionDays:          retentionDays,
		BrainBackupMaxMB:       brainBackupMaxMB,
		Dirs:                   dirs,
		Mods:                   mods,
	}
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

package runner

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
)

// ModelInfo mirrors /api/models items consumed by the Svelte UI.
type ModelInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Badge string `json:"badge"`
}

// ConversationSummary mirrors /api/conversations items.
type ConversationSummary struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count"`
	Model        string `json:"model,omitempty"`
	Engine       string `json:"engine"`
	Pinned       bool   `json:"pinned"`
	Tag          string `json:"tag,omitempty"`
	TagColor     string `json:"tag_color,omitempty"`
	CustomTitle  string `json:"custom_title,omitempty"`
}

// ConversationMessage represents one message in a conversation transcript.
type ConversationMessage struct {
	Role        string   `json:"role"`
	Content     string   `json:"content"`
	Timestamp   string   `json:"timestamp,omitempty"`
	OutboxFiles []string `json:"outbox_files,omitempty"`
}

// ConversationDetail mirrors /api/conversations/{id}.
type ConversationDetail struct {
	ID          string                `json:"id"`
	Title       string                `json:"title,omitempty"`
	UpdatedAt   string                `json:"updated_at,omitempty"`
	Model       string                `json:"model,omitempty"`
	Engine      string                `json:"engine"`
	Pinned      bool                  `json:"pinned,omitempty"`
	Tag         string                `json:"tag,omitempty"`
	TagColor    string                `json:"tag_color,omitempty"`
	ActiveJobID *string               `json:"active_job_id"`
	Messages    []ConversationMessage `json:"messages"`
}

// OutboxFile mirrors /api/outbox file metadata.
type OutboxFile struct {
	Name  string  `json:"name"`
	Size  int64   `json:"size"`
	Mtime float64 `json:"mtime"`
}

// GPUInfo holds NVIDIA GPU telemetry metrics.
type GPUInfo struct {
	Name        string `json:"name"`
	VRAMUsedMB  int    `json:"vram_used_mb"`
	VRAMTotalMB int    `json:"vram_total_mb"`
	TempC       int    `json:"temp_c"`
}

// RAMInfo holds system memory usage in MB.
type RAMInfo struct {
	UsedMB  int `json:"used_mb"`
	TotalMB int `json:"total_mb"`
}

// TelemetryData mirrors /api/telemetry JSON payload.
type TelemetryData struct {
	GPU     *GPUInfo `json:"gpu"`
	CPUTemp *float64 `json:"cpu_temp"`
	RAM     *RAMInfo `json:"ram"`
	Enabled bool     `json:"enabled,omitempty"`
}

// Runner abstracts local (standalone) vs SSH (gateway) execution.
type Runner interface {
	CheckOnline(ctx context.Context, timeout time.Duration) bool
	ActiveOS(ctx context.Context) string
	StartAGYProcess(ctx context.Context, args []string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error)
	OpenOllamaChat(ctx context.Context, payload []byte) (io.ReadCloser, func() error, error)
	ListAGYModels(ctx context.Context) []ModelInfo
	ListOllamaModels(ctx context.Context) []ModelInfo
	ListAGYConversations(ctx context.Context, limit int) ([]ConversationSummary, error)
	GetAGYConversation(ctx context.Context, convID string) (*ConversationDetail, error)
	DeleteAGYConversation(ctx context.Context, convID string) (bool, error)
	SaveToInbox(ctx context.Context, localTmpPath, cleanName string) (string, error)
	ListOutbox(ctx context.Context) ([]OutboxFile, error)
	OpenOutboxFile(ctx context.Context, filename string) (io.ReadCloser, func() error, error)
	DeleteOutboxFile(ctx context.Context, filename string) error
	GetTelemetry(ctx context.Context) TelemetryData
}

// Create builds the configured Runner for standalone or gateway mode.
func Create(cfg config.GatewaySettings) Runner {
	if cfg.Mode == "gateway" {
		return &SSHRunner{cfg: cfg}
	}
	return NewLocalRunner(cfg)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func validateConversationID(convID string) (string, error) {
	clean := strings.TrimSpace(convID)
	if clean == "" || clean == "." || clean == ".." || strings.ContainsAny(clean, `/\`) || filepath.Base(clean) != clean {
		return "", fmt.Errorf("invalid conversation id")
	}
	return clean, nil
}

// ResolveAGYBinary finds the agy CLI executable via AGY_BIN env, PATH, or default install dir.
func ResolveAGYBinary() string {
	if envBin := strings.TrimSpace(os.Getenv("AGY_BIN")); envBin != "" {
		return envBin
	}
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		suffix := ""
		if runtime.GOOS == "windows" {
			suffix = ".exe"
		}
		candidates := []string{
			filepath.Join(home, ".gemini", "bin", "agy"+suffix),
			filepath.Join(home, ".gemini", "antigravity-cli", "bin", "agy"+suffix),
		}
		if runtime.GOOS == "windows" {
			localApp := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
			if localApp == "" {
				localApp = filepath.Join(home, "AppData", "Local")
			}
			candidates = append(candidates, filepath.Join(localApp, "agy", "bin", "agy.exe"))
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return "agy"
}

// --- LocalRunner (Standalone: Linux / macOS / Windows) ---

// LocalRunner executes AGY CLI and Ollama directly on the host machine.
type LocalRunner struct {
	cfg config.GatewaySettings
}

// NewLocalRunner creates a LocalRunner and ensures Inbox/Outbox directories exist.
func NewLocalRunner(cfg config.GatewaySettings) *LocalRunner {
	_ = os.MkdirAll(cfg.Dirs.InboxDir, 0o755)
	_ = os.MkdirAll(cfg.Dirs.OutboxDir, 0o755)
	return &LocalRunner{cfg: cfg}
}

func (r *LocalRunner) brainDir() string {
	if envDir := strings.TrimSpace(os.Getenv("AGY_BRAIN_DIR")); envDir != "" {
		return envDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "antigravity-cli", "brain")
}

func (r *LocalRunner) CheckOnline(_ context.Context, _ time.Duration) bool {
	return true
}

func (r *LocalRunner) ActiveOS(_ context.Context) string {
	return runtime.GOOS
}

func (r *LocalRunner) StartAGYProcess(ctx context.Context, args []string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	bin := ResolveAGYBinary()
	binArgs := append([]string{}, args...)
	if len(binArgs) > 0 && binArgs[0] == "agy" {
		binArgs[0] = bin
	} else {
		binArgs = append([]string{bin}, binArgs...)
	}

	var cmd *exec.Cmd
	// On macOS, wrap long-running AGY commands with caffeinate -i to prevent App Nap/Idle Sleep.
	if runtime.GOOS == "darwin" {
		if caff, err := exec.LookPath("caffeinate"); err == nil {
			wrapped := append([]string{"-i"}, binArgs...)
			cmd = exec.CommandContext(ctx, caff, wrapped...)
		}
	}
	if cmd == nil {
		cmd = exec.CommandContext(ctx, binArgs[0], binArgs[1:]...)
	}
	configureOSProcess(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return cmd, stdout, stderr, nil
}

func (r *LocalRunner) OpenOllamaChat(ctx context.Context, payload []byte) (io.ReadCloser, func() error, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(r.cfg.OllamaURL, "/")+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		return nil, nil, fmt.Errorf("Ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return resp.Body, resp.Body.Close, nil
}

// ParseAGYModelsOutput parses `agy models` stdout into ModelInfo entries.
func ParseAGYModelsOutput(raw string) []ModelInfo {
	normalized := strings.ReplaceAll(raw, "\r", "\n")
	var out []ModelInfo
	seen := map[string]bool{}
	for _, line := range strings.Split(normalized, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "Fetching available models") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		id := fields[0]
		if !strings.Contains(id, "-") || seen[id] {
			continue
		}
		seen[id] = true
		name := strings.TrimSpace(line[len(id):])
		if name == "" {
			name = id
		}
		out = append(out, ModelInfo{
			ID:    id,
			Name:  name,
			Type:  "cloud",
			Badge: deriveAGYModelBadge(id),
		})
	}
	return out
}

func deriveAGYModelBadge(id string) string {
	low := strings.ToLower(id)
	switch {
	case strings.Contains(low, "xhigh"):
		return "Extra"
	case strings.Contains(low, "hydrogen"):
		return "Hydrogen"
	case strings.Contains(low, "argon") || strings.Contains(low, "orchestrator"):
		return "Raciocínio"
	case strings.Contains(low, "flash"):
		return "Rápido"
	case strings.Contains(low, "opus"):
		return "Top"
	case strings.Contains(low, "sonnet"):
		return "Engenharia"
	case strings.Contains(low, "gpt-oss"):
		return "Open"
	default:
		return "Cloud"
	}
}

func (r *LocalRunner) ListAGYModels(ctx context.Context) []ModelInfo {
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, ResolveAGYBinary(), "models")
	configureOSProcess(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return ParseAGYModelsOutput(string(out))
}

func (r *LocalRunner) ListOllamaModels(ctx context.Context) []ModelInfo {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(r.cfg.OllamaURL, "/")+"/api/tags", nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil
	}
	var out []ModelInfo
	for _, m := range parsed.Models {
		if m.Name == "" || strings.HasPrefix(m.Name, "nomic") {
			continue
		}
		out = append(out, ModelInfo{
			ID:    "ollama:" + m.Name,
			Name:  "Local: " + m.Name,
			Type:  "local",
			Badge: "Ollama / Local",
		})
	}
	return out
}

func (r *LocalRunner) ListAGYConversations(_ context.Context, limit int) ([]ConversationSummary, error) {
	brain := r.brainDir()
	entries, err := os.ReadDir(brain)
	if err != nil {
		return []ConversationSummary{}, nil
	}

	summaryTitles := loadSummaryTitles(filepath.Dir(brain))
	items := []ConversationSummary{}

	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		convID := ent.Name()
		transcript := filepath.Join(brain, convID, ".system_generated", "logs", "transcript.jsonl")
		info, err := os.Stat(transcript)
		if err != nil {
			continue
		}
		title := summaryTitles[convID]
		msgCount := 0

		f, err := os.Open(transcript)
		if err == nil {
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
			for scanner.Scan() {
				var row struct {
					Type    string `json:"type"`
					Content string `json:"content"`
				}
				if json.Unmarshal(scanner.Bytes(), &row) != nil {
					continue
				}
				switch row.Type {
				case "USER_INPUT":
					msgCount++
					if title == "" && row.Content != "" {
						title = ExtractConversationTitle(row.Content, "", 55)
					}
				case "PLANNER_RESPONSE":
					if row.Content != "" {
						msgCount++
					}
				}
			}
			_ = f.Close()
		}

		if msgCount > 0 {
			if title == "" {
				title = convID
				if len(title) > 8 {
					title = title[:8]
				}
			}
			items = append(items, ConversationSummary{
				ID:           convID,
				Title:        title,
				UpdatedAt:    info.ModTime().Format(time.RFC3339),
				MessageCount: msgCount,
				Engine:       "agy",
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt > items[j].UpdatedAt
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (r *LocalRunner) GetAGYConversation(_ context.Context, convID string) (*ConversationDetail, error) {
	brain := r.brainDir()
	logsDir := filepath.Join(brain, convID, ".system_generated", "logs")
	transcript := filepath.Join(logsDir, "transcript_full.jsonl")
	if _, err := os.Stat(transcript); err != nil {
		transcript = filepath.Join(logsDir, "transcript.jsonl")
	}
	f, err := os.Open(transcript)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	summaryTitles := loadSummaryTitles(filepath.Dir(brain))
	title := summaryTitles[convID]
	msgs := []ConversationMessage{}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var row struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			continue
		}
		raw := strings.TrimSpace(row.Content)
		if raw == "" {
			continue
		}
		switch row.Type {
		case "USER_INPUT":
			cleaned := CleanUserRequest(raw)
			if title == "" && cleaned != "" {
				title = ExtractConversationTitle(raw, "", 55)
			}
			msgs = append(msgs, ConversationMessage{Role: "user", Content: cleaned})
		case "PLANNER_RESPONSE":
			msgs = append(msgs, ConversationMessage{Role: "agent", Content: raw})
		}
	}
	if title == "" && len(convID) >= 8 {
		title = convID[:8]
	}
	return &ConversationDetail{
		ID:       convID,
		Title:    title,
		Engine:   "agy",
		Messages: msgs,
	}, nil
}

func (r *LocalRunner) DeleteAGYConversation(_ context.Context, convID string) (bool, error) {
	clean, err := validateConversationID(convID)
	if err != nil {
		return false, err
	}
	brain := filepath.Clean(r.brainDir())
	target := filepath.Join(brain, clean)
	if filepath.Dir(target) != brain {
		return false, fmt.Errorf("invalid conversation path")
	}
	info, err := os.Stat(target)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("invalid conversation target")
	}
	if err := backupDirTarGz(brain, clean, target); err != nil {
		return false, fmt.Errorf("backup before delete failed: %w", err)
	}
	if err := os.RemoveAll(target); err != nil {
		return false, err
	}
	return true, nil
}

func backupDirTarGz(brainDir, convID, targetDir string) error {
	backupDir := filepath.Join(filepath.Dir(brainDir), "brain-backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}
	archivePath := filepath.Join(backupDir, fmt.Sprintf("%s_%s.tar.gz", convID, time.Now().Format("20060102-150405")))
	out, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	return filepath.Walk(targetDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(filepath.Dir(targetDir), path)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(tw, f)
			_ = f.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		return nil
	})
}

// --- SSHRunner (Gateway Mode: Pi -> Workstation) ---

// SSHRunner executes AGY CLI, Ollama, and session sync remotely over SSH.
// When WIN_TARGET_USER is configured, it automatically detects whether the host is booted into Linux or Windows.
type SSHRunner struct {
	cfg config.GatewaySettings

	targetMu       sync.RWMutex
	cachedOS       string
	cachedAt       time.Time
	cachedOnline   bool
	cachedOnlineAt time.Time
}

type sshTarget struct {
	osName    string
	host      string
	user      string
	port      string
	agyBin    string
	inboxDir  string
	outboxDir string
}

func (r *SSHRunner) linuxTarget() sshTarget {
	port := r.cfg.TargetSSHPort
	if port == "" {
		port = "22"
	}
	return sshTarget{
		osName:    "linux",
		host:      r.cfg.TargetHost,
		user:      r.cfg.TargetUser,
		port:      port,
		agyBin:    ResolveAGYBinary(),
		inboxDir:  r.cfg.Dirs.InboxDir,
		outboxDir: r.cfg.Dirs.OutboxDir,
	}
}

func (r *SSHRunner) windowsTarget() sshTarget {
	port := r.cfg.WinSSHPort
	if port == "" {
		port = "22"
	}
	agyBin := r.cfg.WinAGYBin
	if agyBin == "" {
		agyBin = "agy"
	}
	inbox := r.cfg.WinInboxDir
	if inbox == "" {
		inbox = fmt.Sprintf("C:/Users/%s/Documents/AGY-Gateway/inbox", r.cfg.WinTargetUser)
	}
	outbox := r.cfg.WinOutboxDir
	if outbox == "" {
		outbox = fmt.Sprintf("C:/Users/%s/Documents/AGY-Gateway/outbox", r.cfg.WinTargetUser)
	}
	return sshTarget{
		osName:    "windows",
		host:      r.cfg.WinTargetHost,
		user:      r.cfg.WinTargetUser,
		port:      port,
		agyBin:    agyBin,
		inboxDir:  inbox,
		outboxDir: outbox,
	}
}

func (r *SSHRunner) hasDualBoot() bool {
	return r.cfg.WinTargetUser != "" && r.cfg.WinTargetHost != ""
}

func (r *SSHRunner) sshBaseArgsFor(t sshTarget, connectTimeoutSec int) []string {
	if connectTimeoutSec <= 0 {
		connectTimeoutSec = 3
	}
	return []string{
		"ssh",
		"-i", r.cfg.SSHKey,
		"-p", t.port,
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", connectTimeoutSec),
		fmt.Sprintf("%s@%s", t.user, t.host),
	}
}

func (r *SSHRunner) probeSSH(ctx context.Context, t sshTarget, cmdStr string) bool {
	cctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	args := append(r.sshBaseArgsFor(t, 2), cmdStr)
	cmd := exec.CommandContext(cctx, args[0], args[1:]...)
	return cmd.Run() == nil
}

func (r *SSHRunner) resolveTarget(ctx context.Context) sshTarget {
	if !r.hasDualBoot() {
		return r.linuxTarget()
	}

	r.targetMu.RLock()
	cached := r.cachedOS
	fresh := time.Since(r.cachedAt) < 15*time.Second
	r.targetMu.RUnlock()
	if fresh && cached != "" {
		if cached == "windows" {
			return r.windowsTarget()
		}
		return r.linuxTarget()
	}

	lt := r.linuxTarget()
	wt := r.windowsTarget()

	detected := "linux"
	if r.probeSSH(ctx, lt, "uname -s") {
		detected = "linux"
	} else if r.probeSSH(ctx, wt, "cmd /c echo ok") {
		detected = "windows"
	}

	r.targetMu.Lock()
	r.cachedOS = detected
	r.cachedAt = time.Now()
	r.targetMu.Unlock()

	if detected == "windows" {
		return wt
	}
	return lt
}

func (r *SSHRunner) ActiveOS(ctx context.Context) string {
	return r.resolveTarget(ctx).osName
}

func (r *SSHRunner) sshBaseArgs(connectTimeoutSec int) []string {
	return r.sshBaseArgsFor(r.resolveTarget(context.Background()), connectTimeoutSec)
}

func (r *SSHRunner) runSSH(ctx context.Context, remoteCmd string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	t := r.resolveTarget(cctx)
	args := append(r.sshBaseArgsFor(t, int(timeout.Seconds())), remoteCmd)
	cmd := exec.CommandContext(cctx, args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *SSHRunner) CheckOnline(ctx context.Context, timeout time.Duration) bool {
	r.targetMu.RLock()
	online := r.cachedOnline
	fresh := !r.cachedOnlineAt.IsZero() && time.Since(r.cachedOnlineAt) < 8*time.Second
	r.targetMu.RUnlock()
	if fresh {
		return online
	}

	t := r.resolveTarget(ctx)
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(t.host, t.port), timeout)
	isUp := err == nil
	if err == nil {
		_ = conn.Close()
	}

	r.targetMu.Lock()
	r.cachedOnline = isUp
	r.cachedOnlineAt = time.Now()
	r.targetMu.Unlock()
	return isUp
}

func winPowerShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func (r *SSHRunner) StartAGYProcess(ctx context.Context, agyArgs []string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	t := r.resolveTarget(ctx)
	var remoteCmd string
	if t.osName == "windows" {
		psArgs := make([]string, len(agyArgs))
		for i, a := range agyArgs {
			if i == 0 && a == "agy" {
				psArgs[i] = winPowerShellQuote(t.agyBin)
			} else {
				psArgs[i] = winPowerShellQuote(a)
			}
		}
		remoteCmd = "powershell -NoProfile -Command \"& " + strings.Join(psArgs, " ") + "\""
	} else {
		quoted := make([]string, len(agyArgs))
		for i, a := range agyArgs {
			quoted[i] = shellQuote(a)
		}
		remoteCmd = strings.Join(quoted, " ")
	}
	args := append(r.sshBaseArgsFor(t, 5), remoteCmd)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}
	return cmd, stdout, stderr, nil
}

func (r *SSHRunner) OpenOllamaChat(ctx context.Context, payload []byte) (io.ReadCloser, func() error, error) {
	remoteCmd := fmt.Sprintf(
		"curl -s -N -X POST %s -d %s",
		shellQuote(strings.TrimRight(r.cfg.OllamaURL, "/")+"/api/chat"),
		shellQuote(string(payload)),
	)
	args := append(r.sshBaseArgs(5), remoteCmd)
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	return stdout, cmd.Wait, nil
}

func (r *SSHRunner) ListAGYModels(ctx context.Context) []ModelInfo {
	t := r.resolveTarget(ctx)
	cmdStr := t.agyBin + " models"
	if t.osName == "windows" {
		cmdStr = "powershell -NoProfile -Command \"& " + winPowerShellQuote(t.agyBin) + " models\""
	}
	stdout, err := r.runSSH(ctx, cmdStr, 8*time.Second)
	if err != nil || stdout == "" {
		return nil
	}
	return ParseAGYModelsOutput(stdout)
}

func (r *SSHRunner) ListOllamaModels(ctx context.Context) []ModelInfo {
	remoteCmd := fmt.Sprintf("curl -s --max-time 2 %s", shellQuote(strings.TrimRight(r.cfg.OllamaURL, "/")+"/api/tags"))
	stdout, err := r.runSSH(ctx, remoteCmd, 3*time.Second)
	if err != nil || stdout == "" {
		return nil
	}
	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.Unmarshal([]byte(stdout), &parsed) != nil {
		return nil
	}
	var out []ModelInfo
	for _, m := range parsed.Models {
		if m.Name == "" || strings.HasPrefix(m.Name, "nomic") {
			continue
		}
		out = append(out, ModelInfo{
			ID:    "ollama:" + m.Name,
			Name:  "Local: " + m.Name,
			Type:  "local",
			Badge: "Ollama / GPU",
		})
	}
	return out
}

func (r *SSHRunner) ListAGYConversations(ctx context.Context, limit int) ([]ConversationSummary, error) {
	stdout, err := r.runSSH(ctx, "python3 /home/ai/bin/agy-gateway-sync.py list "+strconv.Itoa(limit), 6*time.Second)
	if err != nil || stdout == "" {
		return []ConversationSummary{}, nil
	}
	var parsed struct {
		Conversations []ConversationSummary `json:"conversations"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		return []ConversationSummary{}, nil
	}
	for i := range parsed.Conversations {
		parsed.Conversations[i].Engine = "agy"
	}
	return parsed.Conversations, nil
}

func (r *SSHRunner) GetAGYConversation(ctx context.Context, convID string) (*ConversationDetail, error) {
	stdout, err := r.runSSH(ctx, "python3 /home/ai/bin/agy-gateway-sync.py get "+shellQuote(convID), 5*time.Second)
	if err != nil || stdout == "" {
		return nil, nil
	}
	var detail ConversationDetail
	if err := json.Unmarshal([]byte(stdout), &detail); err != nil {
		return nil, nil
	}
	if detail.Messages == nil {
		return nil, nil
	}
	detail.Engine = "agy"
	return &detail, nil
}

func (r *SSHRunner) DeleteAGYConversation(ctx context.Context, convID string) (bool, error) {
	clean, err := validateConversationID(convID)
	if err != nil {
		return false, err
	}
	pyCmd := fmt.Sprintf(
		`python3 -c 'import os,shutil,sys,tarfile,time; bid=sys.argv[1]; b=os.path.normpath(os.environ.get("AGY_BRAIN_DIR") or os.path.expanduser("~/.gemini/antigravity-cli/brain")); p=os.path.join(b, bid); exists=os.path.isdir(p); bak_dir=os.path.join(os.path.dirname(b), "brain-backups"); (os.makedirs(bak_dir, exist_ok=True), tarfile.open(os.path.join(bak_dir, f"{bid}_{time.strftime(\"%%Y%%m%%d-%%H%%M%%S\")}.tar.gz"), "w:gz").add(p, arcname=bid), shutil.rmtree(p)) if exists else None; print("1" if exists else "0")' %s`,
		shellQuote(clean),
	)
	out, err := r.runSSH(ctx, pyCmd, 5*time.Second)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "1", nil
}

package runner

import (
	"bufio"
	"bytes"
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
}

// ConversationMessage represents one message in a conversation transcript.
type ConversationMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
}

// ConversationDetail mirrors /api/conversations/{id}.
type ConversationDetail struct {
	ID          string                `json:"id"`
	Title       string                `json:"title,omitempty"`
	UpdatedAt   string                `json:"updated_at,omitempty"`
	Model       string                `json:"model,omitempty"`
	Engine      string                `json:"engine"`
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
	StartAGYProcess(ctx context.Context, args []string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error)
	OpenOllamaChat(ctx context.Context, payload []byte) (io.ReadCloser, func() error, error)
	ListOllamaModels(ctx context.Context) []ModelInfo
	ListAGYConversations(ctx context.Context, limit int) ([]ConversationSummary, error)
	GetAGYConversation(ctx context.Context, convID string) (*ConversationDetail, error)
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

// ResolveAGYBinary finds the agy CLI executable via AGY_BIN env, PATH, or default install dir.
func ResolveAGYBinary() string {
	if envBin := strings.TrimSpace(os.Getenv("AGY_BIN")); envBin != "" {
		return envBin
	}
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".gemini", "antigravity-cli", "bin", "agy")
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
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
	transcript := filepath.Join(brain, convID, ".system_generated", "logs", "transcript.jsonl")
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

// --- SSHRunner (Gateway Mode: Pi -> Workstation) ---

// SSHRunner executes AGY CLI, Ollama, and session sync remotely over SSH.
type SSHRunner struct {
	cfg config.GatewaySettings
}

func (r *SSHRunner) sshBaseArgs(connectTimeoutSec int) []string {
	return []string{
		"ssh",
		"-i", r.cfg.SSHKey,
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", connectTimeoutSec),
		fmt.Sprintf("%s@%s", r.cfg.TargetUser, r.cfg.TargetHost),
	}
}

func (r *SSHRunner) runSSH(ctx context.Context, remoteCmd string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append(r.sshBaseArgs(int(timeout.Seconds())), remoteCmd)
	cmd := exec.CommandContext(cctx, args[0], args[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (r *SSHRunner) CheckOnline(_ context.Context, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(r.cfg.TargetHost, "22"), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (r *SSHRunner) StartAGYProcess(ctx context.Context, agyArgs []string) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	quoted := make([]string, len(agyArgs))
	for i, a := range agyArgs {
		quoted[i] = shellQuote(a)
	}
	args := append(r.sshBaseArgs(5), strings.Join(quoted, " "))
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

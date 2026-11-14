package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ideBridgeRequest struct {
	JobID          string  `json:"job_id"`
	ConversationID string  `json:"conversation_id"`
	Prompt         string  `json:"prompt"`
	Model          string  `json:"model"`
	PermissionMode string  `json:"permission_mode"`
	CreatedAt      string  `json:"created_at"`
}

type ideBridgeResponse struct {
	ConversationID string `json:"conversation_id,omitempty"`
	Status         string `json:"status,omitempty"`
	Response       string `json:"response,omitempty"`
	Error          string `json:"error,omitempty"`
}

// runIDEBridgeJob writes prompts to <IDE_BRIDGE_DIR>/inbox/<job_id>.json and waits for outbox/<job_id>.json.
// This lets IDE-only setups (without standalone CLI) integrate via workspace watchers or MCP bridge scripts.
func (m *Manager) runIDEBridgeJob(ctx context.Context, jobID string, convID *string, prompt, selectedModel, permMode string) {
	defer m.cleanupJob(jobID)

	bridgeBase := m.cfg.IDEBridgeDir
	inboxDir := filepath.Join(bridgeBase, "inbox")
	outboxDir := filepath.Join(bridgeBase, "outbox")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		m.failJob(jobID, "", ctx, fmt.Sprintf("Erro ao criar IDE bridge inbox: %v", err))
		return
	}
	if err := os.MkdirAll(outboxDir, 0o755); err != nil {
		m.failJob(jobID, "", ctx, fmt.Sprintf("Erro ao criar IDE bridge outbox: %v", err))
		return
	}

	cid := "ide-" + jobID
	if convID != nil && *convID != "" {
		cid = *convID
	}
	_ = m.store.UpdateJob(jobID, &cid, nil, nil)
	m.emitEvent(jobID, "init", map[string]any{"type": "init", "conversation_id": cid, "model": selectedModel})
	m.emitEvent(jobID, "tool", map[string]any{
		"type":    "tool",
		"name":    "ide_bridge",
		"state":   "ACTIVE",
		"preview": filepath.Join(inboxDir, jobID+".json"),
	})

	reqPayload, _ := json.MarshalIndent(ideBridgeRequest{
		JobID:          jobID,
		ConversationID: cid,
		Prompt:         prompt,
		Model:          selectedModel,
		PermissionMode: permMode,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	reqFile := filepath.Join(inboxDir, jobID+".json")
	if err := os.WriteFile(reqFile, reqPayload, 0o644); err != nil {
		m.failJob(jobID, cid, ctx, fmt.Sprintf("Erro ao gravar payload na ponte IDE: %v", err))
		return
	}

	timeoutSec := m.cfg.IDEBridgeTimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = 180
	}
	deadline := time.NewTimer(time.Duration(timeoutSec) * time.Second)
	defer deadline.Stop()

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	outFile := filepath.Join(outboxDir, jobID+".json")
	for {
		select {
		case <-ctx.Done():
			_ = os.Remove(reqFile)
			m.failJob(jobID, cid, ctx, "Execução cancelada pelo usuário.")
			return
		case <-deadline.C:
			m.failJob(jobID, cid, ctx, fmt.Sprintf("Timeout (%ds) aguardando resposta da IDE em %s", timeoutSec, outFile))
			return
		case <-ticker.C:
			raw, err := os.ReadFile(outFile)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				m.failJob(jobID, cid, ctx, fmt.Sprintf("Erro ao ler resposta da IDE: %v", err))
				return
			}
			var resp ideBridgeResponse
			if json.Unmarshal(raw, &resp) != nil {
				continue
			}
			_ = os.Remove(reqFile)
			_ = os.Remove(outFile)

			if resp.ConversationID != "" {
				cid = resp.ConversationID
			}
			if resp.Error != "" {
				m.failJob(jobID, cid, ctx, resp.Error)
				return
			}
			text := strings.TrimSpace(resp.Response)
			if text == "" {
				text = "*Tarefa processada via IDE Bridge.*"
			}
			m.emitEvent(jobID, "delta", map[string]any{"type": "delta", "text": text})
			doneStatus := "completed"
			_ = m.store.UpdateJob(jobID, &cid, &doneStatus, nil)
			m.emitEvent(jobID, "done", map[string]any{
				"type":            "done",
				"conversation_id": cid,
				"status":          "SUCCESS",
				"executed_tools":  []string{"ide_bridge"},
				"response":        text,
			})
			go m.sendNtfy("AGY (IDE Bridge) Concluído", text, cid, "default")
			return
		}
	}
}

package automation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/db"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/jobs"
	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/runner"
)

// DockerState mirrors container state inspected by hardware alerts and check tasks.
type DockerState struct {
	Name   string
	State  string
	Status string
}

// Scheduler coordinates SQLite Cron tasks and active GPU/disk/Docker alert rules.
type Scheduler struct {
	cfg      config.GatewaySettings
	store    *db.GatewayDB
	runner   runner.Runner
	jobMgr   *jobs.Manager
	notifier *Notifier

	nowFn        func() time.Time
	dockerListFn func(ctx context.Context) (bool, []DockerState)
	diskFn       func(path string) (DiskSpaceInfo, error)

	mu              sync.Mutex
	triggeredMinute map[string]string
	runningCron     map[string]bool
	lastGPUAlert    time.Time
	lastDiskAlert   time.Time
	dockerSeeded    bool
	prevRunning     map[string]bool
}

// NewScheduler initializes the gateway Cron & active hardware alert engine.
func NewScheduler(
	cfg config.GatewaySettings,
	store *db.GatewayDB,
	r runner.Runner,
	mgr *jobs.Manager,
	notifier *Notifier,
) *Scheduler {
	s := &Scheduler{
		cfg:             cfg,
		store:           store,
		runner:          r,
		jobMgr:          mgr,
		notifier:        notifier,
		nowFn:           time.Now,
		diskFn:          GetDiskSpace,
		triggeredMinute: map[string]string{},
		runningCron:     map[string]bool{},
		prevRunning:     map[string]bool{},
	}
	s.dockerListFn = s.defaultListDocker
	return s
}

// Start launches the Cron tick (20s), hardware/Docker watcher (30s), and storage retention cleanup (6h) loops until ctx cancels.
func (s *Scheduler) Start(ctx context.Context) {
	s.RecomputeAllNextRuns()

	cronTicker := time.NewTicker(20 * time.Second)
	watchTicker := time.NewTicker(30 * time.Second)
	retentionTicker := time.NewTicker(6 * time.Hour)
	go func() {
		defer cronTicker.Stop()
		defer watchTicker.Stop()
		defer retentionTicker.Stop()

		if rep := RunRetentionCleanup(s.cfg, s.nowFn()); rep.InboxRemoved+rep.OutboxRemoved+rep.BackupsRemoved > 0 {
			log.Printf("[retention] cleaned inbox=%d outbox=%d backups=%d (%d bytes)",
				rep.InboxRemoved, rep.OutboxRemoved, rep.BackupsRemoved, rep.TotalBytesReclaimed)
		}
		s.TickWatchers(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-cronTicker.C:
				s.TickCron(ctx)
			case <-watchTicker.C:
				s.TickWatchers(ctx)
			case <-retentionTicker.C:
				if rep := RunRetentionCleanup(s.cfg, s.nowFn()); rep.InboxRemoved+rep.OutboxRemoved+rep.BackupsRemoved > 0 {
					log.Printf("[retention] cleaned inbox=%d outbox=%d backups=%d (%d bytes)",
						rep.InboxRemoved, rep.OutboxRemoved, rep.BackupsRemoved, rep.TotalBytesReclaimed)
				}
			}
		}
	}()
}

// RecomputeAllNextRuns ensures every enabled automation has an up-to-date next_run_at on startup.
func (s *Scheduler) RecomputeAllNextRuns() {
	list, err := s.store.ListAutomations()
	if err != nil {
		return
	}
	now := s.nowFn()
	for _, a := range list {
		if !a.Enabled {
			continue
		}
		sched, err := ParseCron(a.CronExpr)
		if err != nil {
			continue
		}
		if next, ok := sched.NextAfter(now); ok {
			_ = s.store.UpdateAutomationRunState(a.ID, a.LastRunAt, a.LastStatus, a.LastSummary, a.LastJobID, a.LastConvID, next.Format(time.RFC3339))
		}
	}
}

// TickCron evaluates all enabled Cron tasks for the current minute.
func (s *Scheduler) TickCron(ctx context.Context) {
	list, err := s.store.ListAutomations()
	if err != nil {
		return
	}
	now := s.nowFn()
	minuteKey := now.Format("2006-01-02T15:04")

	for _, a := range list {
		if !a.Enabled {
			continue
		}
		sched, err := ParseCron(a.CronExpr)
		if err != nil || !sched.Matches(now) {
			continue
		}

		s.mu.Lock()
		alreadyMin := s.triggeredMinute[a.ID] == minuteKey
		isRunning := s.runningCron[a.ID]
		if !alreadyMin && !isRunning {
			s.triggeredMinute[a.ID] = minuteKey
			s.runningCron[a.ID] = true
		}
		s.mu.Unlock()

		if alreadyMin || isRunning {
			continue
		}
		go s.executeAutomation(ctx, a, sched)
	}
}

// RunAutomationNow executes a saved automation immediately on demand.
func (s *Scheduler) RunAutomationNow(ctx context.Context, id string) (*db.Automation, error) {
	a, err := s.store.GetAutomation(id)
	if err != nil || a == nil {
		return nil, fmt.Errorf("automação não encontrada")
	}
	sched, err := ParseCron(a.CronExpr)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.runningCron[a.ID] {
		s.mu.Unlock()
		return nil, fmt.Errorf("automação já está em execução")
	}
	s.runningCron[a.ID] = true
	s.mu.Unlock()

	go s.executeAutomation(ctx, *a, sched)
	a.LastStatus = "running"
	return a, nil
}

func (s *Scheduler) nextRunStr(sched *CronSchedule) string {
	if sched == nil {
		return ""
	}
	if next, ok := sched.NextAfter(s.nowFn()); ok {
		return next.Format(time.RFC3339)
	}
	return ""
}

func (s *Scheduler) executeAutomation(ctx context.Context, a db.Automation, sched *CronSchedule) {
	defer func() {
		s.mu.Lock()
		delete(s.runningCron, a.ID)
		s.mu.Unlock()
	}()

	runAt := s.nowFn().UTC().Format(time.RFC3339)
	nextAt := s.nextRunStr(sched)
	_ = s.store.UpdateAutomationRunState(a.ID, runAt, "running", a.LastSummary, a.LastJobID, a.LastConvID, nextAt)

	if a.Kind == "check" {
		s.runNativeCheckAutomation(ctx, a, runAt, nextAt)
		return
	}
	s.runPromptAutomation(ctx, a, runAt, nextAt)
}

func parseCheckTargets(raw string) (disk, docker, gpu bool) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if !strings.HasPrefix(trimmed, "checks:") {
		return true, true, true
	}
	spec := strings.TrimPrefix(trimmed, "checks:")
	if before, _, ok := strings.Cut(spec, "|"); ok {
		spec = before
	}
	for _, tok := range strings.Split(spec, ",") {
		switch strings.TrimSpace(tok) {
		case "disk", "disco":
			disk = true
		case "docker":
			docker = true
		case "gpu":
			gpu = true
		}
	}
	if !disk && !docker && !gpu {
		return true, true, true
	}
	return disk, docker, gpu
}

func (s *Scheduler) runNativeCheckAutomation(ctx context.Context, a db.Automation, runAt, nextAt string) {
	cfg, _ := s.store.GetAlertConfig()
	wantDisk, wantDocker, wantGPU := parseCheckTargets(a.Prompt)
	var issues []string
	var parts []string

	if wantDisk {
		if dInfo, err := s.diskFn(s.cfg.Dirs.DataDir); err == nil && dInfo.TotalGB > 0 {
			parts = append(parts, fmt.Sprintf("Disco %.1f GB livres (%.0f%%)", dInfo.FreeGB, dInfo.FreePct))
			minPct := cfg.DiskFreeMinPct
			if minPct <= 0 {
				minPct = 10
			}
			if dInfo.FreePct <= float64(minPct) {
				issues = append(issues, fmt.Sprintf("Espaço em disco baixo: %.1f%% livre", dInfo.FreePct))
			}
		}
	}

	if wantDocker {
		if dockerOK, ctrs := s.dockerListFn(ctx); dockerOK {
			runningCount := 0
			var down []string
			for _, c := range ctrs {
				if c.State == "running" {
					runningCount++
				} else {
					down = append(down, fmt.Sprintf("%s (%s)", c.Name, c.State))
				}
			}
			parts = append(parts, fmt.Sprintf("Docker: %d ativos", runningCount))
			if len(down) > 0 {
				parts = append(parts, "Parados/erro: "+strings.Join(down, ", "))
				issues = append(issues, fmt.Sprintf("%d container(s) fora de execução: %s", len(down), strings.Join(down, ", ")))
			}
		}
	}

	if wantGPU {
		tel := s.runner.GetTelemetry(ctx)
		if tel.GPU != nil {
			parts = append(parts, fmt.Sprintf("GPU %d°C · VRAM %d/%d MB", tel.GPU.TempC, tel.GPU.VRAMUsedMB, tel.GPU.VRAMTotalMB))
			if cfg.GPUTempThresholdC > 0 && tel.GPU.TempC >= cfg.GPUTempThresholdC {
				issues = append(issues, fmt.Sprintf("GPU em %d°C (>= %d°C)", tel.GPU.TempC, cfg.GPUTempThresholdC))
			}
		}
	}

	if len(parts) == 0 {
		parts = append(parts, "Check executado sem métricas adicionais.")
	}
	summary := strings.Join(parts, " | ")
	status := "ok"
	severity := "ok"
	title := fmt.Sprintf("Cron OK: %s", a.Name)
	if len(issues) > 0 {
		status = "error"
		severity = "warning"
		title = fmt.Sprintf("Cron Alerta: %s", a.Name)
		summary = strings.Join(issues, "; ") + " — " + summary
	}

	_ = s.store.UpdateAutomationRunState(a.ID, runAt, status, summary, "", "", nextAt)

	if a.NotifyOn == "error_only" && len(issues) == 0 {
		return
	}
	ntfyFlag := a.NotifyNtfy
	pushFlag := a.NotifyPush
	s.notifier.Dispatch(ctx, AlertInput{
		Source:   "cron",
		Severity: severity,
		Title:    title,
		Message:  summary,
		SendNtfy: &ntfyFlag,
		SendPush: &pushFlag,
	})
}

func (s *Scheduler) runPromptAutomation(ctx context.Context, a db.Automation, runAt, nextAt string) {
	model := a.Model
	perm := a.PermissionMode
	if perm == "" {
		perm = "readonly"
	}
	if !s.runner.CheckOnline(ctx, 1500*time.Millisecond) {
		errMsg := fmt.Sprintf("Host (%s) está inacessível.", s.cfg.TargetHost)
		_ = s.store.UpdateAutomationRunState(a.ID, runAt, "error", errMsg, "", "", nextAt)
		ntfyFlag := a.NotifyNtfy
		pushFlag := a.NotifyPush
		s.notifier.Dispatch(ctx, AlertInput{
			Source:   "cron",
			Severity: "error",
			Title:    fmt.Sprintf("Cron Falhou: %s", a.Name),
			Message:  errMsg,
			SendNtfy: &ntfyFlag,
			SendPush: &pushFlag,
		})
		return
	}
	res, err := s.jobMgr.StartJob(ctx, jobs.CreateRequest{
		Prompt:         a.Prompt,
		Model:          &model,
		PermissionMode: &perm,
	})
	if err != nil {
		errMsg := err.Error()
		_ = s.store.UpdateAutomationRunState(a.ID, runAt, "error", errMsg, "", "", nextAt)
		ntfyFlag := a.NotifyNtfy
		pushFlag := a.NotifyPush
		s.notifier.Dispatch(ctx, AlertInput{
			Source:   "cron",
			Severity: "error",
			Title:    fmt.Sprintf("Cron Falhou: %s", a.Name),
			Message:  errMsg,
			SendNtfy: &ntfyFlag,
			SendPush: &pushFlag,
		})
		return
	}

	jobID, _ := res["job_id"].(string)
	_ = s.store.UpdateAutomationRunState(a.ID, runAt, "running", a.LastSummary, jobID, a.LastConvID, nextAt)

	status, summary, convID := s.awaitJobSummary(ctx, jobID)
	finalStatus := "ok"
	severity := "ok"
	title := fmt.Sprintf("Cron Concluído: %s", a.Name)
	if status != "completed" {
		finalStatus = "error"
		severity = "error"
		title = fmt.Sprintf("Cron Erro: %s", a.Name)
	}
	_ = s.store.UpdateAutomationRunState(a.ID, runAt, finalStatus, summary, jobID, convID, nextAt)

	if a.NotifyOn == "error_only" && finalStatus == "ok" {
		return
	}
	ntfyFlag := a.NotifyNtfy
	pushFlag := a.NotifyPush
	s.notifier.Dispatch(ctx, AlertInput{
		Source:   "cron",
		Severity: severity,
		Title:    title,
		Message:  summary,
		ConvID:   convID,
		JobID:    jobID,
		SendNtfy: &ntfyFlag,
		SendPush: &pushFlag,
	})
}

func (s *Scheduler) awaitJobSummary(ctx context.Context, jobID string) (status, summary, convID string) {
	deadline := time.NewTimer(15 * time.Minute)
	defer deadline.Stop()

	for {
		j, err := s.store.GetJob(jobID)
		if err == nil && j != nil && j.Status != "running" {
			if j.ConversationID != nil {
				convID = *j.ConversationID
			}
			if j.Error != nil && *j.Error != "" {
				return j.Status, *j.Error, convID
			}
			events, _ := s.store.GetJobEventsAfter(jobID, 0)
			var textBuf strings.Builder
			for _, ev := range events {
				if ev.EventType == "delta" {
					if t, ok := ev.Payload["text"].(string); ok {
						textBuf.WriteString(t)
					}
				}
			}
			out := strings.TrimSpace(textBuf.String())
			if len([]rune(out)) > 400 {
				out = string([]rune(out)[:400]) + "..."
			}
			if out == "" {
				out = "Automação executada."
			}
			return j.Status, out, convID
		}

		sig := s.jobMgr.WaitSignal(jobID)
		select {
		case <-ctx.Done():
			return "error", "Gateway encerrado durante execução.", convID
		case <-deadline.C:
			return "error", "Tempo limite (15m) excedido para automação.", convID
		case <-sig:
		}
	}
}

// TickWatchers runs active checks for GPU temperature, disk free space, and stopped Docker containers.
func (s *Scheduler) TickWatchers(ctx context.Context) {
	cfg, err := s.store.GetAlertConfig()
	if err != nil {
		return
	}
	now := s.nowFn()

	// 1. GPU Temperature threshold with 10m cooldown
	if cfg.GPUTempThresholdC > 0 {
		tel := s.runner.GetTelemetry(ctx)
		if tel.GPU != nil && tel.GPU.TempC >= cfg.GPUTempThresholdC {
			s.mu.Lock()
			cool := now.Sub(s.lastGPUAlert) >= 10*time.Minute
			if cool {
				s.lastGPUAlert = now
			}
			s.mu.Unlock()
			if cool {
				s.notifier.Dispatch(ctx, AlertInput{
					Source:   "gpu",
					Severity: "warning",
					Priority: "high",
					Title:    fmt.Sprintf("GPU em %d°C", tel.GPU.TempC),
					Message:  fmt.Sprintf("%s atingiu %d°C (limite %d°C · VRAM %d/%d MB).", tel.GPU.Name, tel.GPU.TempC, cfg.GPUTempThresholdC, tel.GPU.VRAMUsedMB, tel.GPU.VRAMTotalMB),
				})
			}
		}
	}

	// 2. Disk Space minimum threshold with 30m cooldown
	if cfg.DiskFreeMinPct > 0 {
		if dInfo, err := s.diskFn(s.cfg.Dirs.DataDir); err == nil && dInfo.TotalGB > 0 && dInfo.FreePct <= float64(cfg.DiskFreeMinPct) {
			s.mu.Lock()
			cool := now.Sub(s.lastDiskAlert) >= 30*time.Minute
			if cool {
				s.lastDiskAlert = now
			}
			s.mu.Unlock()
			if cool {
				s.notifier.Dispatch(ctx, AlertInput{
					Source:   "disk",
					Severity: "warning",
					Priority: "high",
					Title:    "Espaço em disco baixo",
					Message:  fmt.Sprintf("Apenas %.1f GB (%.1f%%) livres em %s (mínimo configurado: %d%%).", dInfo.FreeGB, dInfo.FreePct, dInfo.Path, cfg.DiskFreeMinPct),
				})
			}
		}
	}

	// 3. Docker container crash/drop transition watcher
	if cfg.NotifyDockerDown {
		dockerOK, ctrs := s.dockerListFn(ctx)
		if dockerOK {
			curRunning := map[string]bool{}
			stateByName := map[string]DockerState{}
			for _, c := range ctrs {
				stateByName[c.Name] = c
				if c.State == "running" {
					curRunning[c.Name] = true
				}
			}

			var dropped []string
			s.mu.Lock()
			if !s.dockerSeeded {
				s.dockerSeeded = true
			} else {
				for name := range s.prevRunning {
					if !curRunning[name] {
						if st, ok := stateByName[name]; ok {
							dropped = append(dropped, fmt.Sprintf("%s (%s)", name, st.State))
						} else {
							dropped = append(dropped, fmt.Sprintf("%s (removido/offline)", name))
						}
					}
				}
			}
			s.prevRunning = curRunning
			s.mu.Unlock()

			if len(dropped) > 0 {
				s.notifier.Dispatch(ctx, AlertInput{
					Source:   "docker",
					Severity: "error",
					Priority: "high",
					Title:    "Container Docker caiu",
					Message:  "Mudança detectada de running para parado: " + strings.Join(dropped, ", "),
				})
			}
		}
	}
}

func (s *Scheduler) defaultListDocker(ctx context.Context) (bool, []DockerState) {
	// If Cockpit optional package is enabled, fetch container state directly from the workstation.
	if enabled, _ := s.cfg.Mods["cockpit"].(bool); enabled && s.cfg.CockpitURL != "" {
		cctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, strings.TrimRight(s.cfg.CockpitURL, "/")+"/overview", nil)
		if err == nil {
			if s.cfg.CockpitToken != "" {
				req.Header.Set("Authorization", "Bearer "+s.cfg.CockpitToken)
			}
			resp, err := (&http.Client{Timeout: 2500 * time.Millisecond}).Do(req)
			cancel()
			if err == nil {
				var parsed struct {
					DockerAvailable bool `json:"docker_available"`
					Containers      []struct {
						Name   string `json:"name"`
						State  string `json:"state"`
						Status string `json:"status"`
					} `json:"containers"`
				}
				decErr := json.NewDecoder(resp.Body).Decode(&parsed)
				_ = resp.Body.Close()
				if decErr == nil && parsed.DockerAvailable {
					out := make([]DockerState, 0, len(parsed.Containers))
					for _, c := range parsed.Containers {
						out = append(out, DockerState{
							Name:   c.Name,
							State:  strings.ToLower(c.State),
							Status: c.Status,
						})
					}
					return true, out
				}
			}
		} else {
			cancel()
		}
	}

	if _, err := exec.LookPath("docker"); err != nil {
		return false, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "docker", "ps", "-a", "--format", "{{.Names}}\t{{.State}}\t{{.Status}}")
	configureOSProcess(cmd)
	raw, err := cmd.Output()
	if err != nil {
		return false, nil
	}
	var out []DockerState
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		parts := strings.SplitN(strings.TrimSpace(sc.Text()), "\t", 3)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		st := ""
		if len(parts) >= 3 {
			st = parts[2]
		}
		out = append(out, DockerState{
			Name:   parts[0],
			State:  strings.ToLower(parts[1]),
			Status: st,
		})
	}
	return true, out
}

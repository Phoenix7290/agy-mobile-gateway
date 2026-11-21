package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
)

func envTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func shouldRunConsoleController(goos string, args []string, headlessEnv, noConsoleEnv string, interactive bool) bool {
	if slices.Contains(args, "--daemon") || envTruthy(headlessEnv) {
		return false
	}
	if slices.Contains(args, "--console") {
		return true
	}
	// Interactive console panel is automatic only on Windows (double-click UX).
	// On Linux/macOS, running agy-gateway without flags keeps standard foreground server behavior.
	if goos != "windows" {
		return false
	}
	if envTruthy(noConsoleEnv) || !interactive {
		return false
	}
	return true
}

func maybeRunConsoleController() bool {
	args := os.Args[1:]

	if slices.Contains(args, "--autostart") || hasArgPrefix(args, "--autostart=") {
		handleAutostartCLI(args)
		return true
	}

	if !shouldRunConsoleController(
		runtime.GOOS,
		args,
		os.Getenv("AGY_HEADLESS"),
		os.Getenv("AGY_NO_CONSOLE"),
		isInteractiveConsole(),
	) {
		return false
	}

	ensureDefaultConfigEnv()
	cfg := config.Load()

	if isGatewayHealthyLocal(cfg.Port) && strings.TrimSpace(os.Getenv("NO_SINGLE_INSTANCE_OPEN")) == "" {
		prepareConsoleDisplay()
		fmt.Printf("AGY Mobile Gateway já está rodando em http://127.0.0.1:%s. Abrindo no navegador...\n", cfg.Port)
		openGatewayAppWindow(cfg.Port)
		time.Sleep(1100 * time.Millisecond)
		return true
	}

	runControlConsoleUI(cfg)
	return true
}

func hasArgPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

func handleAutostartCLI(args []string) {
	action := "status"
	for i, a := range args {
		if a == "--autostart" && i+1 < len(args) {
			action = strings.ToLower(strings.TrimSpace(args[i+1]))
			break
		}
		if strings.HasPrefix(a, "--autostart=") {
			action = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(a, "--autostart=")))
			break
		}
	}

	ensureDefaultConfigEnv()
	cfg := config.Load()
	exePath, err := os.Executable()
	if err != nil {
		exePath = "agy-gateway"
	}
	workDir := resolveConsoleWorkDir(cfg.Dirs.DataDir)

	switch action {
	case "on", "enable", "true", "1":
		if err := setSystemAutoStart(true, exePath, workDir, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Erro ao ativar auto-start: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Auto-Start ativado com sucesso para o logon do sistema.")
	case "off", "disable", "false", "0":
		if err := setSystemAutoStart(false, exePath, workDir, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "Erro ao desativar auto-start: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Auto-Start desativado.")
	default:
		on, detail := isSystemAutoStartEnabled()
		state := "OFF"
		if on {
			state = "ON"
		}
		fmt.Printf("Auto-Start: %s (%s)\n", state, detail)
	}
}

func generateCockpitToken() string {
	var buf [24]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("agy-cockpit-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func ensureDefaultConfigEnv() {
	if _, custom := os.LookupEnv("DATA_DIR"); custom {
		return
	}
	cfgDir := config.DefaultConfigDir()
	envPath := filepath.Join(cfgDir, ".env")
	if _, err := os.Stat(envPath); err == nil {
		return
	}
	// Do not write inside unit tests or go-build temp runs
	if exe, err := os.Executable(); err == nil {
		if strings.Contains(strings.ToLower(exe), "go-build") {
			return
		}
	}
	if _, err := os.Stat(".env"); err == nil {
		return
	}
	_ = os.MkdirAll(cfgDir, 0o755)
	workDir := resolveConsoleWorkDir(cfgDir)
	cockpitToken := generateCockpitToken()
	content := strings.Join([]string{
		"GATEWAY_MODE=standalone",
		"PORT=8088",
		"ENABLE_TERMINAL=true",
		"ENABLE_TELEMETRY=true",
		"ENABLE_COCKPIT=true",
		"ENABLE_OLLAMA=true",
		"COCKPIT_ADDR=127.0.0.1:8099",
		"COCKPIT_URL=http://127.0.0.1:8099",
		"COCKPIT_ALLOW_POWER=false",
		"COCKPIT_TOKEN=" + cockpitToken,
		"TTYD_WS_URL=ws://127.0.0.1:7681/terminal/ws",
		"AGY_WORKSPACE=" + workDir,
		"",
	}, "\n")
	_ = os.WriteFile(envPath, []byte(content), 0o600)
}

type supervisedChild struct {
	name string
	cmd  *exec.Cmd
	logF *os.File
}

type consoleSupervisor struct {
	cfg     config.GatewaySettings
	workDir string
	exePath string
	logDir  string
	handle  supervisorPlatformHandle

	mu         sync.Mutex
	children   []*supervisedChild
	lastNotice string
}

func newConsoleSupervisor(cfg config.GatewaySettings) *consoleSupervisor {
	exe, err := os.Executable()
	if err != nil {
		exe = "agy-gateway"
	}
	return &consoleSupervisor{
		cfg:     cfg,
		workDir: resolveConsoleWorkDir(cfg.Dirs.DataDir),
		exePath: exe,
		logDir:  filepath.Join(cfg.Dirs.DataDir, "logs"),
		handle:  newSupervisorPlatformHandle(),
	}
}

func buildTtydArgs(goos, workDir, shellExe string) []string {
	args := []string{"-i", "127.0.0.1", "-p", "7681", "-b", "/terminal", "-W", "-t", "fontSize=14", "-w", workDir, shellExe}
	base := strings.ToLower(filepath.Base(shellExe))
	if goos == "windows" && (strings.Contains(base, "powershell") || base == "pwsh.exe" || base == "pwsh") {
		args = append(args, "-NoLogo")
	}
	return args
}

func (s *consoleSupervisor) startSidecarsLocked() {
	_ = os.MkdirAll(s.logDir, 0o755)
	exeDir := filepath.Dir(s.exePath)

	// 1. Optional ttyd terminal (bound strictly to loopback so LAN clients must go through gateway proxy)
	if enabled, _ := s.cfg.Mods["terminal"].(bool); enabled && !isLocalPortOpen(7681) {
		if ttydBin, err := exec.LookPath("ttyd"); err == nil {
			var shellExe string
			if runtime.GOOS == "windows" {
				shellExe = "powershell.exe"
				if pwsh, err := exec.LookPath("pwsh"); err == nil {
					shellExe = pwsh
				}
			} else {
				shellExe = strings.TrimSpace(os.Getenv("SHELL"))
				if shellExe == "" {
					if _, err := exec.LookPath("bash"); err == nil {
						shellExe = "bash"
					} else {
						shellExe = "sh"
					}
				}
			}
			ttydArgs := buildTtydArgs(runtime.GOOS, s.workDir, shellExe)
			_ = s.spawnLocked("ttyd", ttydBin, ttydArgs, s.workDir, nil, filepath.Join(s.logDir, "ttyd.log"))
		}
	}

	// 2. Optional Cockpit daemon
	if enabled, _ := s.cfg.Mods["cockpit"].(bool); enabled && !isLocalPortOpen(8099) {
		cockpitName := "agy-cockpit"
		if runtime.GOOS == "windows" {
			cockpitName = "agy-cockpit.exe"
		}
		cockpitCandidates := []string{
			filepath.Join(exeDir, cockpitName),
			filepath.Join(s.workDir, "packages", "cockpit", cockpitName),
		}
		for _, c := range cockpitCandidates {
			if _, err := os.Stat(c); err == nil {
				extraEnv := []string{
					"COCKPIT_ADDR=127.0.0.1:8099",
				}
				if strings.TrimSpace(os.Getenv("COCKPIT_ALLOW_POWER")) == "" {
					extraEnv = append(extraEnv, "COCKPIT_ALLOW_POWER=false")
				}
				if s.cfg.CockpitToken != "" {
					extraEnv = append(extraEnv, "COCKPIT_TOKEN="+s.cfg.CockpitToken)
				}
				_ = s.spawnLocked("agy-cockpit", c, nil, exeDir, extraEnv, filepath.Join(s.logDir, "cockpit.log"))
				break
			}
		}
	}
}

func (s *consoleSupervisor) startAll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if isGatewayHealthyLocal(s.cfg.Port) && len(s.children) == 0 {
		s.lastNotice = fmt.Sprintf("Gateway já estava ativo em :%s (aberto externamente).", s.cfg.Port)
		return
	}

	s.startSidecarsLocked()

	if !isLocalPortOpenStr(s.cfg.Port) {
		extraEnv := []string{"AGY_HEADLESS=1"}
		if err := s.spawnLocked("agy-gateway", s.exePath, []string{"--daemon"}, s.workDir, extraEnv, filepath.Join(s.logDir, "gateway.err.log")); err != nil {
			s.lastNotice = "Erro ao iniciar Gateway: " + err.Error()
			return
		}
		ready := false
		for range 30 {
			if isGatewayHealthyLocal(s.cfg.Port) {
				ready = true
				break
			}
			time.Sleep(120 * time.Millisecond)
		}
		// Give sibling sidecars a short grace window to finish binding before first paint
		if !isLocalPortOpen(8099) || !isLocalPortOpen(7681) {
			time.Sleep(250 * time.Millisecond)
		}
		if ready {
			s.lastNotice = "AGY iniciado e pronto para uso."
		} else {
			s.lastNotice = "Gateway iniciado (aguardando resposta HTTP...)."
		}
	} else {
		s.lastNotice = "Serviços já estavam em execução."
	}
}

func (s *consoleSupervisor) spawnLocked(name, bin string, args []string, dir string, extraEnv []string, logPath string) error {
	for _, c := range s.children {
		if c.name == name && c.cmd != nil && c.cmd.ProcessState == nil {
			return nil
		}
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		f = nil
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), extraEnv...)
	setChildProcAttrs(cmd)
	if f != nil {
		cmd.Stdout = f
		cmd.Stderr = f
	}
	if err := cmd.Start(); err != nil {
		if f != nil {
			_ = f.Close()
		}
		return err
	}
	s.handle.assignChild(cmd)

	child := &supervisedChild{name: name, cmd: cmd, logF: f}
	s.children = append(s.children, child)

	go func(ch *supervisedChild) {
		_ = cmd.Wait()
		if ch.logF != nil {
			_ = ch.logF.Close()
		}
	}(child)
	return nil
}

func (s *consoleSupervisor) stopAll() {
	s.mu.Lock()
	children := s.children
	s.children = nil
	s.mu.Unlock()

	for _, ch := range children {
		terminateChild(ch.cmd)
		if ch.logF != nil {
			_ = ch.logF.Close()
		}
	}
	time.Sleep(180 * time.Millisecond)
	if isLocalPortOpenStr(s.cfg.Port) || isLocalPortOpen(8099) || isLocalPortOpen(7681) {
		cleanupPortsFallback(s.cfg.Port)
		time.Sleep(150 * time.Millisecond)
	}
	s.mu.Lock()
	s.lastNotice = "Tudo encerrado. Modo privacidade ativo."
	s.mu.Unlock()
}

func (s *consoleSupervisor) stopOwnedChildrenOnly() {
	s.mu.Lock()
	children := s.children
	s.children = nil
	s.mu.Unlock()

	for _, ch := range children {
		terminateChild(ch.cmd)
		if ch.logF != nil {
			_ = ch.logF.Close()
		}
	}
	s.handle.closeHandle()
}

func (s *consoleSupervisor) setNotice(msg string) {
	s.mu.Lock()
	s.lastNotice = msg
	s.mu.Unlock()
}

func (s *consoleSupervisor) statusNotice() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastNotice
}

func runControlConsoleUI(cfg config.GatewaySettings) {
	prepareConsoleDisplay()

	sup := newConsoleSupervisor(cfg)
	defer func() {
		sup.stopAll()
		sup.handle.closeHandle()
	}()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, consoleSignals()...)

	sup.startAll()

	restoreKeys, keyCh := startConsoleKeyReader()
	defer restoreKeys()

	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	renderConsoleScreen(sup)

	for {
		select {
		case <-sigCh:
			fmt.Println("\nEncerrando serviços e fechando...")
			return
		case ch, ok := <-keyCh:
			if !ok {
				return
			}
			switch strings.ToLower(string(ch)) {
			case "1", "l":
				sup.startAll()
				renderConsoleScreen(sup)
			case "2", "d":
				sup.stopAll()
				renderConsoleScreen(sup)
			case "3", "r":
				sup.stopAll()
				sup.startAll()
				renderConsoleScreen(sup)
			case "4", "o":
				if isGatewayHealthyLocal(cfg.Port) {
					openGatewayAppWindow(cfg.Port)
					sup.setNotice(fmt.Sprintf("Navegador aberto em http://127.0.0.1:%s", cfg.Port))
				} else {
					sup.setNotice("Gateway desligado. Pressione [1] para ligar primeiro.")
				}
				renderConsoleScreen(sup)
			case "5":
				showLogTail(filepath.Join(sup.logDir, "gateway.err.log"), 14)
				fmt.Print("\nPressione qualquer tecla para voltar ao painel...")
				<-keyCh
				renderConsoleScreen(sup)
			case "6":
				renderConsoleScreen(sup)
			case "7", "a":
				cur, _ := isSystemAutoStartEnabled()
				target := !cur
				if err := setSystemAutoStart(target, sup.exePath, sup.workDir, sup.cfg); err != nil {
					sup.setNotice("Erro ao alterar Auto-Start: " + err.Error())
				} else if target {
					sup.setNotice("Auto-Start ATIVADO: vai iniciar sozinho ao ligar/logar no sistema.")
				} else {
					sup.setNotice("Auto-Start DESATIVADO: só roda quando você clicar.")
				}
				renderConsoleScreen(sup)
			case "8":
				msg, err := createAppShortcuts(sup.exePath, sup.workDir)
				if err != nil {
					sup.setNotice("Erro ao criar atalho: " + err.Error())
				} else {
					sup.setNotice(msg)
				}
				renderConsoleScreen(sup)
			case "q", "\x1b":
				fmt.Println("\nDesligando AGY e fechando terminal...")
				return
			}
		case <-ticker.C:
			renderConsoleScreen(sup)
		}
	}
}

func renderConsoleScreen(sup *consoleSupervisor) {
	gwUp := isGatewayHealthyLocal(sup.cfg.Port)
	cockpitUp := isLocalPortOpen(8099)
	ttydUp := isLocalPortOpen(7681)
	autoOn, autoDesc := isSystemAutoStartEnabled()
	lanIPs := detectLANIPs()

	fmt.Print("\033[2J\033[H")
	fmt.Println("================================================================")
	fmt.Println("  AGY Mobile Gateway - Controle de Execução e Privacidade")
	fmt.Println("================================================================")
	if gwUp {
		fmt.Println("  Status Geral : \033[32m[ RODANDO ]\033[0m  (Fechar terminal encerra tudo)")
	} else {
		fmt.Println("  Status Geral : \033[31m[ DESLIGADO ]\033[0m  (Privacidade ativa)")
	}
	if autoOn {
		fmt.Printf("  Auto-Start   : \033[32m[ ON ]\033[0m  %s\n", autoDesc)
	} else {
		fmt.Println("  Auto-Start   : \033[90m[ OFF ]\033[0m Ao iniciar o sistema (use [7] para mudar)")
	}
	fmt.Printf("  Workspace    : %s\n", sup.workDir)
	fmt.Println("----------------------------------------------------------------")
	fmt.Printf("  Gateway (:%s) : %s\n", sup.cfg.Port, formatOnOff(gwUp))
	fmt.Printf("  Cockpit (:8099) : %s\n", formatOnOff(cockpitUp))
	fmt.Printf("  Terminal(:7681) : %s\n", formatOnOff(ttydUp))
	fmt.Println("----------------------------------------------------------------")
	if gwUp {
		fmt.Printf("  Abrir no PC      : http://127.0.0.1:%s\n", sup.cfg.Port)
		if len(lanIPs) > 0 {
			fmt.Printf("  Abrir no Celular : http://%s:%s\n", lanIPs[0], sup.cfg.Port)
			for _, extra := range lanIPs[1:] {
				fmt.Printf("  Outro IP / VPN   : http://%s:%s\n", extra, sup.cfg.Port)
			}
		} else {
			fmt.Println("  Abrir no Celular : (nenhum IP LAN detectado)")
		}
	} else {
		fmt.Println("  Nenhuma porta do AGY está ativa neste momento.")
	}
	fmt.Println("----------------------------------------------------------------")
	fmt.Println("  [1] Ligar tudo            [2] Desligar tudo (Privacidade)")
	fmt.Println("  [3] Reiniciar             [4] Abrir no Navegador")
	fmt.Println("  [5] Ver log recente       [6] Atualizar status")
	fmt.Println("  [7] Auto-Start no boot    [8] Criar Atalho App (Desktop/Menu)")
	fmt.Println("  [Q] Desligar e Fechar terminal")
	fmt.Println("================================================================")
	if msg := sup.statusNotice(); msg != "" {
		fmt.Printf("  Info: %s\n", msg)
	}
}

func formatOnOff(up bool) string {
	if up {
		return "\033[32mON\033[0m"
	}
	return "\033[90mOFF\033[0m"
}

func isLocalPortOpenStr(portStr string) bool {
	p, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || p <= 0 {
		return false
	}
	return isLocalPortOpen(p)
}

func isLocalPortOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 180*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func resolveConsoleWorkDir(fallback string) string {
	if envDir := strings.TrimSpace(os.Getenv("AGY_WORKSPACE")); envDir != "" {
		if info, err := os.Stat(envDir); err == nil && info.IsDir() {
			return envDir
		}
	}
	if wd, err := os.Getwd(); err == nil {
		lower := strings.ToLower(wd)
		isSystemDir := lower == "/" ||
			strings.Contains(lower, `\windows\system32`) ||
			strings.Contains(lower, `\appdata\local\agy\bin`)
		if !isSystemDir {
			return wd
		}
	}
	return fallback
}

func detectLANIPs() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	type candidate struct {
		ip       string
		priority int
	}
	var found []candidate
	for _, iface := range ifaces {
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}
		nameLower := strings.ToLower(iface.Name)
		if strings.Contains(nameLower, "wsl") ||
			strings.Contains(nameLower, "vethernet") ||
			strings.Contains(nameLower, "docker") ||
			strings.HasPrefix(nameLower, "br-") ||
			strings.HasPrefix(nameLower, "veth") ||
			strings.Contains(nameLower, "bluetooth") ||
			strings.Contains(nameLower, "virtual") ||
			strings.Contains(nameLower, "vmware") ||
			strings.Contains(nameLower, "vbox") ||
			strings.Contains(nameLower, "proton") ||
			strings.Contains(nameLower, "openvpn") ||
			strings.HasPrefix(nameLower, "utun") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			v4 := ipNet.IP.To4()
			if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
				continue
			}
			ipStr := v4.String()
			prio := 50
			if strings.HasPrefix(ipStr, "192.168.") {
				prio = 10
			} else if strings.HasPrefix(ipStr, "10.") {
				prio = 20
			} else if strings.HasPrefix(ipStr, "172.") {
				prio = 30
			} else if strings.Contains(nameLower, "radmin") || strings.Contains(nameLower, "tailscale") {
				prio = 40
			}
			found = append(found, candidate{ip: ipStr, priority: prio})
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].priority != found[j].priority {
			return found[i].priority < found[j].priority
		}
		return found[i].ip < found[j].ip
	})
	var out []string
	seen := map[string]bool{}
	for _, c := range found {
		if !seen[c.ip] {
			seen[c.ip] = true
			out = append(out, c.ip)
		}
	}
	return out
}

func showLogTail(path string, maxLines int) {
	fmt.Print("\033[2J\033[H")
	fmt.Printf("=== Log recente (%s) ===\n\n", path)
	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("(sem log disponível: %v)\n", err)
		return
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, 128*1024))
	if err != nil {
		return
	}
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	if len(lines) == 0 {
		fmt.Println("(arquivo de log vazio)")
		return
	}
	for _, l := range lines {
		fmt.Println(l)
	}
}

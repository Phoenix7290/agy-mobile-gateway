//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
)

const (
	linuxServiceName = "agy-gateway.service"
	darwinAgentLabel = "com.agy.mobile-gateway"
)

type supervisorPlatformHandle struct{}

func newSupervisorPlatformHandle() supervisorPlatformHandle {
	return supervisorPlatformHandle{}
}

func (supervisorPlatformHandle) assignChild(*exec.Cmd) {}

func (*supervisorPlatformHandle) closeHandle() {}

func setChildProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateChild(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Terminate the child's entire process group cleanly first
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	time.Sleep(150 * time.Millisecond)
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func consoleSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}

func cleanupPortsFallback(_ string) {
	// Intentionally no-op on Unix: never run indiscriminate fuser/lsof kills by port,
	// because it can terminate unrelated services or conflict with systemd user units.
	// Owned child process groups are terminated explicitly by terminateChild().
}

func prepareConsoleDisplay() {
	fmt.Printf("\033]0;AGY Mobile Gateway - Painel de Controle\007")
}

func isInteractiveConsole() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return false
	}
	fo, err := os.Stdout.Stat()
	if err != nil || (fo.Mode()&os.ModeCharDevice) == 0 {
		return false
	}
	return termiosSupported()
}

func openGatewayAppWindow(port string) {
	targetURL := "http://127.0.0.1:" + port
	if runtime.GOOS == "darwin" {
		_ = exec.Command("open", targetURL).Start()
		return
	}
	_ = exec.Command("xdg-open", targetURL).Start()
}

func isSystemAutoStartEnabled() (bool, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, ""
	}
	if runtime.GOOS == "darwin" {
		plist := filepath.Join(home, "Library", "LaunchAgents", darwinAgentLabel+".plist")
		if _, err := os.Stat(plist); err == nil {
			return true, "LaunchAgent (" + darwinAgentLabel + ")"
		}
		return false, ""
	}
	unit := filepath.Join(home, ".config", "systemd", "user", linuxServiceName)
	if _, err := exec.LookPath("systemctl"); err == nil {
		if err := exec.Command("systemctl", "--user", "is-enabled", linuxServiceName).Run(); err == nil {
			return true, "systemd --user (" + linuxServiceName + ")"
		}
	}
	if _, err := os.Stat(unit); err == nil {
		return true, "systemd unit (" + unit + ")"
	}
	desktopAutostart := filepath.Join(home, ".config", "autostart", "agy-gateway.desktop")
	if _, err := os.Stat(desktopAutostart); err == nil {
		return true, "XDG autostart (" + desktopAutostart + ")"
	}
	return false, ""
}

func setSystemAutoStart(enable bool, exePath, workDir string, cfg config.GatewaySettings) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		return setDarwinLaunchAgent(enable, home, exePath, workDir, cfg)
	}
	return setLinuxAutoStart(enable, home, exePath, workDir)
}

func setDarwinLaunchAgent(enable bool, home, exePath, workDir string, cfg config.GatewaySettings) error {
	agentsDir := filepath.Join(home, "Library", "LaunchAgents")
	plistPath := filepath.Join(agentsDir, darwinAgentLabel+".plist")
	uid := fmt.Sprintf("gui/%d", os.Getuid())

	if !enable {
		_ = exec.Command("launchctl", "bootout", uid, plistPath).Run()
		_ = exec.Command("launchctl", "unload", "-w", plistPath).Run()
		_ = os.Remove(plistPath)
		return nil
	}

	ensureDefaultConfigEnv()
	logDir := filepath.Join(cfg.Dirs.DataDir, "logs")
	_ = os.MkdirAll(agentsDir, 0o755)
	_ = os.MkdirAll(logDir, 0o755)

	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>--daemon</string>
    <string>--with-sidecars</string>
  </array>
  <key>WorkingDirectory</key>
  <string>%s</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>AGY_HEADLESS</key>
    <string>1</string>
    <key>PATH</key>
    <string>%s</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, darwinAgentLabel, xmlEscape(exePath), xmlEscape(workDir), xmlEscape(os.Getenv("PATH")),
		xmlEscape(filepath.Join(logDir, "gateway.log")),
		xmlEscape(filepath.Join(logDir, "gateway.err.log")),
	)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	if exec.Command("launchctl", "bootstrap", uid, plistPath).Run() != nil {
		if out, err := exec.Command("launchctl", "load", "-w", plistPath).CombinedOutput(); err != nil {
			return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func setLinuxAutoStart(enable bool, home, exePath, workDir string) error {
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	unitPath := filepath.Join(unitDir, linuxServiceName)
	xdgDir := filepath.Join(home, ".config", "autostart")
	xdgPath := filepath.Join(xdgDir, "agy-gateway.desktop")

	if !enable {
		_ = exec.Command("systemctl", "--user", "disable", "--now", linuxServiceName).Run()
		_ = os.Remove(unitPath)
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		_ = os.Remove(xdgPath)
		return nil
	}

	ensureDefaultConfigEnv()
	if _, err := exec.LookPath("systemctl"); err == nil {
		_ = os.MkdirAll(unitDir, 0o755)
		unitBody := fmt.Sprintf(`[Unit]
Description=AGY Mobile Gateway
After=network-online.target

[Service]
Type=simple
WorkingDirectory=%s
Environment="AGY_HEADLESS=1"
ExecStart="%s" --daemon --with-sidecars
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`, workDir, exePath)
		if err := os.WriteFile(unitPath, []byte(unitBody), 0o644); err != nil {
			return err
		}
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
		if err := exec.Command("systemctl", "--user", "enable", linuxServiceName).Run(); err == nil {
			return nil
		}
	}

	// Fallback for non-systemd desktop sessions (Alpine / i3 / container desktops)
	_ = os.MkdirAll(xdgDir, 0o755)
	desktopEntry := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=AGY Mobile Gateway
Comment=AGY Mobile Gateway background service
Exec="%s" --daemon --with-sidecars
Path=%s
Terminal=false
X-GNOME-Autostart-enabled=true
`, exePath, workDir)
	return os.WriteFile(xdgPath, []byte(desktopEntry), 0o644)
}

func createAppShortcuts(exePath, workDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		appsDir := filepath.Join(home, "Applications")
		deskDir := filepath.Join(home, "Desktop")
		_ = os.MkdirAll(appsDir, 0o755)
		scriptBody := fmt.Sprintf("#!/bin/bash\ncd %q || true\nexec %q --console\n", workDir, exePath)
		cmdApp := filepath.Join(appsDir, "AGY Gateway.command")
		if err := os.WriteFile(cmdApp, []byte(scriptBody), 0o755); err != nil {
			return "", err
		}
		if info, err := os.Stat(deskDir); err == nil && info.IsDir() {
			_ = os.WriteFile(filepath.Join(deskDir, "AGY Gateway.command"), []byte(scriptBody), 0o755)
		}
		return "Atalho 'AGY Gateway.command' criado em ~/Applications e ~/Desktop.", nil
	}

	appsDir := filepath.Join(home, ".local", "share", "applications")
	_ = os.MkdirAll(appsDir, 0o755)
	desktopBody := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=AGY Mobile Gateway
Comment=Painel interativo do AGY Mobile Gateway
Exec="%s" --console
Path=%s
Terminal=true
Categories=Development;Utility;
`, exePath, workDir)
	appFile := filepath.Join(appsDir, "agy-gateway.desktop")
	if err := os.WriteFile(appFile, []byte(desktopBody), 0o755); err != nil {
		return "", err
	}
	deskDir := filepath.Join(home, "Desktop")
	if out, err := exec.Command("xdg-user-dir", "DESKTOP").Output(); err == nil {
		if p := strings.TrimSpace(string(out)); p != "" {
			deskDir = p
		}
	}
	if info, err := os.Stat(deskDir); err == nil && info.IsDir() {
		_ = os.WriteFile(filepath.Join(deskDir, "agy-gateway.desktop"), []byte(desktopBody), 0o755)
	}
	return "Atalho 'AGY Mobile Gateway' criado no Menu de Apps e Desktop.", nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

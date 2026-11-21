//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
	"golang.org/x/sys/windows"
)

const (
	createNoWindowFlag = 0x08000000
	winTaskName        = "AGY-Mobile-Gateway"
)

var (
	kernel32DLL         = windows.NewLazySystemDLL("kernel32.dll")
	procSetConsoleTitle = kernel32DLL.NewProc("SetConsoleTitleW")
	procSetOutputCP     = kernel32DLL.NewProc("SetConsoleOutputCP")
)

type supervisorPlatformHandle struct {
	job windows.Handle
}

func newSupervisorPlatformHandle() supervisorPlatformHandle {
	job, _ := createKillOnCloseJob()
	return supervisorPlatformHandle{job: job}
}

func createKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func (h supervisorPlatformHandle) assignChild(cmd *exec.Cmd) {
	if h.job == 0 || cmd == nil || cmd.Process == nil {
		return
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	_ = windows.AssignProcessToJobObject(h.job, proc)
}

func (h *supervisorPlatformHandle) closeHandle() {
	if h.job != 0 {
		_ = windows.CloseHandle(h.job)
		h.job = 0
	}
}

func setChildProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindowFlag}
}

func terminateChild(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func consoleSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func cleanupPortsFallback(port string) {
	_ = exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command",
		"$p8088=Get-NetTCPConnection -LocalPort "+port+" -State Listen -EA SilentlyContinue | Select -First 1 -Expand OwningProcess; if($p8088){Stop-Process -Id $p8088 -Force -EA SilentlyContinue}; "+
			"$p8099=Get-NetTCPConnection -LocalPort 8099 -State Listen -EA SilentlyContinue | Select -First 1 -Expand OwningProcess; if($p8099){Stop-Process -Id $p8099 -Force -EA SilentlyContinue}; "+
			"$p7681=Get-NetTCPConnection -LocalPort 7681 -State Listen -EA SilentlyContinue | Select -First 1 -Expand OwningProcess; if($p7681){Stop-Process -Id $p7681 -Force -EA SilentlyContinue}",
	).Run()
}

func prepareConsoleDisplay() {
	_, _, _ = procSetOutputCP.Call(65001)
	if ptr, err := windows.UTF16PtrFromString("AGY Mobile Gateway - Painel de Controle"); err == nil {
		_, _, _ = procSetConsoleTitle.Call(uintptr(unsafe.Pointer(ptr)))
	}
	var origOutMode uint32
	outHandle := windows.Handle(os.Stdout.Fd())
	if windows.GetConsoleMode(outHandle, &origOutMode) == nil {
		_ = windows.SetConsoleMode(outHandle, origOutMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
	}
}

func isInteractiveConsole() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return false
	}
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode); err != nil {
		return false
	}
	var outMode uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdout.Fd()), &outMode); err != nil {
		return false
	}
	return true
}

func startConsoleKeyReader() (func(), <-chan byte) {
	ch := make(chan byte, 16)
	inHandle := windows.Handle(os.Stdin.Fd())
	var origMode uint32
	rawEnabled := false
	if windows.GetConsoleMode(inHandle, &origMode) == nil {
		rawMode := origMode &^ (windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT)
		if windows.SetConsoleMode(inHandle, rawMode) == nil {
			rawEnabled = true
		}
	}

	restore := func() {
		if rawEnabled {
			_ = windows.SetConsoleMode(inHandle, origMode)
		}
	}

	go func() {
		defer close(ch)
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			ch <- buf[0]
		}
	}()
	return restore, ch
}

func openGatewayAppWindow(port string) {
	targetURL := "http://127.0.0.1:" + port
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", targetURL).Start()
}

func isSystemAutoStartEnabled() (bool, string) {
	out, err := exec.Command("schtasks", "/Query", "/TN", winTaskName).CombinedOutput()
	if err == nil && !strings.Contains(strings.ToLower(string(out)), "error") {
		return true, "Task Scheduler (" + winTaskName + ")"
	}
	startupLnk := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "AGY Mobile Gateway.lnk")
	if _, statErr := os.Stat(startupLnk); statErr == nil {
		return true, "Startup (" + startupLnk + ")"
	}
	return false, ""
}

func setSystemAutoStart(enable bool, exePath, workDir string, cfg config.GatewaySettings) error {
	binDir := filepath.Dir(exePath)
	startupVbs := filepath.Join(binDir, "agy-startup.vbs")
	startupLnk := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "AGY Mobile Gateway.lnk")

	if !enable {
		_ = exec.Command("schtasks", "/Delete", "/TN", winTaskName, "/F").Run()
		_ = os.Remove(startupLnk)
		_ = os.Remove(startupVbs)
		return nil
	}

	ensureDefaultConfigEnv()
	vbsBody := fmt.Sprintf(`CreateObject("WScript.Shell").Run """%s"" --daemon --with-sidecars", 0, False`, exePath) + "\r\n"
	if err := os.WriteFile(startupVbs, []byte(vbsBody), 0o644); err != nil {
		return err
	}

	psCmd := fmt.Sprintf(
		`$action = New-ScheduledTaskAction -Execute "$env:SystemRoot\System32\wscript.exe" -Argument '//B //NoLogo "%s"' -WorkingDirectory '%s'; `+
			`$trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME; `+
			`$principal = New-ScheduledTaskPrincipal -UserId $env:USERNAME -LogonType Interactive -RunLevel Limited; `+
			`$settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit (New-TimeSpan -Seconds 0) -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -StartWhenAvailable; `+
			`Register-ScheduledTask -TaskName '%s' -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description 'AGY Mobile Gateway em segundo plano' -Force | Out-Null`,
		escapeSingleQuotesPS(startupVbs), escapeSingleQuotesPS(workDir), winTaskName,
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command", psCmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		// Fallback to Startup shortcut if Task Scheduler is restricted by policy
		fallbackPS := fmt.Sprintf(
			`$w=New-Object -ComObject WScript.Shell; $s=$w.CreateShortcut('%s'); $s.TargetPath="$env:SystemRoot\System32\wscript.exe"; $s.Arguments='//B //NoLogo "%s"'; $s.WorkingDirectory='%s'; $s.Save()`,
			escapeSingleQuotesPS(startupLnk), escapeSingleQuotesPS(startupVbs), escapeSingleQuotesPS(workDir),
		)
		if fbOut, fbErr := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command", fallbackPS).CombinedOutput(); fbErr != nil {
			return fmt.Errorf("%v (%s / %s)", err, strings.TrimSpace(string(out)), strings.TrimSpace(string(fbOut)))
		}
	}
	_ = cfg
	return nil
}

func createAppShortcuts(exePath, workDir string) (string, error) {
	iconPath := filepath.Join(filepath.Dir(exePath), "agy-gateway.ico")
	if _, err := os.Stat(iconPath); err != nil {
		iconPath = exePath
	}
	startLnk := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "AGY.lnk")
	psScript := fmt.Sprintf(
		`$w = New-Object -ComObject WScript.Shell; `+
			`$desk = $w.SpecialFolders('Desktop'); `+
			`foreach ($dest in @('%s', (Join-Path $desk 'AGY.lnk'))) { `+
			`  if (-not (Split-Path $dest)) { continue }; `+
			`  $s = $w.CreateShortcut($dest); `+
			`  $s.TargetPath = '%s'; `+
			`  $s.Arguments = '--console'; `+
			`  $s.WorkingDirectory = '%s'; `+
			`  $s.IconLocation = '%s'; `+
			`  $s.Save() `+
			`}`,
		escapeSingleQuotesPS(startLnk),
		escapeSingleQuotesPS(exePath),
		escapeSingleQuotesPS(workDir),
		escapeSingleQuotesPS(iconPath),
	)
	out, err := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command", psScript).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	return "Atalhos 'AGY' criados no Menu Iniciar e Área de Trabalho.", nil
}

func escapeSingleQuotesPS(s string) string {
	return strings.ReplaceAll(s, `'`, `''`)
}

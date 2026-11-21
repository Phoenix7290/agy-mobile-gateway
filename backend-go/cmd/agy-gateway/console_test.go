package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestConsoleHelpersCrossPlatform(t *testing.T) {
	customDir := t.TempDir()
	t.Setenv("AGY_WORKSPACE", customDir)
	if got := resolveConsoleWorkDir("fallback"); got != customDir {
		t.Fatalf("expected AGY_WORKSPACE=%q, got %q", customDir, got)
	}

	h := newSupervisorPlatformHandle()
	h.closeHandle()

	logFile := filepath.Join(customDir, "gateway.err.log")
	if err := os.WriteFile(logFile, []byte("linha1\nlinha2\nlinha3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	showLogTail(logFile, 2)

	// LAN IP detection should never panic and exclude loopbacks
	for _, ip := range detectLANIPs() {
		if ip == "127.0.0.1" {
			t.Fatalf("loopback IP should be filtered from detectLANIPs")
		}
	}
}

func TestShouldRunConsoleController(t *testing.T) {
	cases := []struct {
		name        string
		goos        string
		args        []string
		headlessEnv string
		noConsole   string
		interactive bool
		want        bool
	}{
		{"windows interactive default", "windows", nil, "", "", true, true},
		{"windows non-interactive", "windows", nil, "", "", false, false},
		{"windows daemon flag", "windows", []string{"--daemon"}, "", "", true, false},
		{"windows headless env", "windows", nil, "1", "", true, false},
		{"windows no console env", "windows", nil, "", "true", true, false},
		{"linux default stays foreground server", "linux", nil, "", "", true, false},
		{"darwin default stays foreground server", "darwin", nil, "", "", true, false},
		{"linux explicit --console flag", "linux", []string{"--console"}, "", "", true, true},
		{"linux --daemon overrides --console", "linux", []string{"--daemon", "--console"}, "", "", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRunConsoleController(tc.goos, tc.args, tc.headlessEnv, tc.noConsole, tc.interactive)
			if got != tc.want {
				t.Fatalf("shouldRunConsoleController(%q, %v, %q, %q, %v) = %v, want %v",
					tc.goos, tc.args, tc.headlessEnv, tc.noConsole, tc.interactive, got, tc.want)
			}
		})
	}
}

func TestBuildTtydArgsBindsLoopback(t *testing.T) {
	for _, tc := range []struct {
		goos  string
		shell string
	}{
		{"linux", "/bin/bash"},
		{"windows", "powershell.exe"},
	} {
		args := buildTtydArgs(tc.goos, "/tmp", tc.shell)
		idx := slices.Index(args, "-i")
		if idx < 0 || idx+1 >= len(args) || args[idx+1] != "127.0.0.1" {
			t.Fatalf("expected -i 127.0.0.1 in ttyd args for %s, got %v", tc.goos, args)
		}
	}
}

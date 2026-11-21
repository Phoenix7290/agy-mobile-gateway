package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsDotEnvFromDataDir(t *testing.T) {
	tmp := t.TempDir()
	envFile := filepath.Join(tmp, ".env")
	content := "ENABLE_TERMINAL=true\nENABLE_TELEMETRY=true\nENABLE_COCKPIT=true\nPORT=19088\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_DIR", tmp)

	cfg := Load()
	if cfg.Port != "19088" {
		t.Fatalf("expected PORT=19088 from DataDir .env, got %q", cfg.Port)
	}
	if cfg.Mods["terminal"] != true || cfg.Mods["telemetry"] != true || cfg.Mods["cockpit"] != true {
		t.Fatalf("expected terminal/telemetry/cockpit enabled from DataDir .env, got %+v", cfg.Mods)
	}
	if DefaultConfigDir() == "" {
		t.Fatalf("expected non-empty DefaultConfigDir()")
	}
}

func TestLoadRetentionAndBOMHandling(t *testing.T) {
	tmp := t.TempDir()
	envFile := filepath.Join(tmp, ".env")
	// Simulate PowerShell 5.1 UTF-8 BOM on the first line
	content := "\ufeffRETENTION_DAYS=7\nBRAIN_BACKUP_MAX_MB=128\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_DIR", tmp)
	t.Setenv("RETENTION_DAYS", "")
	t.Setenv("BRAIN_BACKUP_MAX_MB", "")
	_ = os.Unsetenv("RETENTION_DAYS")
	_ = os.Unsetenv("BRAIN_BACKUP_MAX_MB")

	cfg := Load()
	if cfg.RetentionDays != 7 {
		t.Fatalf("expected RETENTION_DAYS=7 (after BOM strip), got %d", cfg.RetentionDays)
	}
	if cfg.BrainBackupMaxMB != 128 {
		t.Fatalf("expected BRAIN_BACKUP_MAX_MB=128, got %d", cfg.BrainBackupMaxMB)
	}
}

package automation

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
)

func TestRetentionCleanupTTLAndSizeCap(t *testing.T) {
	tmp := t.TempDir()
	inbox := filepath.Join(tmp, "inbox")
	outbox := filepath.Join(tmp, "outbox")
	backups := filepath.Join(tmp, "brain-backups")
	for _, d := range []string{inbox, outbox, backups} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	oldTime := now.Add(-40 * 24 * time.Hour)
	freshTime := now.Add(-5 * 24 * time.Hour)

	writeAged := func(path string, payload []byte, mtime time.Time) {
		t.Helper()
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	writeAged(filepath.Join(inbox, "old.png"), []byte("12345"), oldTime)
	writeAged(filepath.Join(inbox, "fresh.png"), []byte("12345"), freshTime)
	writeAged(filepath.Join(inbox, ".gitkeep"), []byte(""), oldTime)
	writeAged(filepath.Join(outbox, "old.pdf"), []byte("123456"), oldTime)
	writeAged(filepath.Join(outbox, "fresh.pdf"), []byte("123456"), freshTime)
	writeAged(filepath.Join(backups, "expired.tar.gz"), []byte("aaaa"), oldTime)

	// Two fresh backups totaling ~2MB to test BrainBackupMaxMB=1 cap (oldest should be removed)
	big1 := make([]byte, 700*1024)
	big2 := make([]byte, 700*1024)
	writeAged(filepath.Join(backups, "older-fresh.tar.gz"), big1, now.Add(-10*time.Hour))
	writeAged(filepath.Join(backups, "newest-fresh.tar.gz"), big2, now.Add(-1*time.Hour))

	cfg := config.GatewaySettings{
		RetentionDays:    30,
		BrainBackupMaxMB: 1,
		Dirs: config.AppDirs{
			InboxDir:  inbox,
			OutboxDir: outbox,
		},
	}

	rep := RunRetentionCleanupWithBackupsDir(cfg, backups, now)
	if rep.InboxRemoved != 1 || rep.OutboxRemoved != 1 || rep.BackupsRemoved != 2 {
		t.Fatalf("unexpected cleanup report: %+v", rep)
	}

	// Verify kept files
	for _, mustExist := range []string{
		filepath.Join(inbox, "fresh.png"),
		filepath.Join(inbox, ".gitkeep"),
		filepath.Join(outbox, "fresh.pdf"),
		filepath.Join(backups, "newest-fresh.tar.gz"),
	} {
		if _, err := os.Stat(mustExist); err != nil {
			t.Fatalf("expected %s to remain, got err: %v", mustExist, err)
		}
	}
	for _, mustBeGone := range []string{
		filepath.Join(inbox, "old.png"),
		filepath.Join(outbox, "old.pdf"),
		filepath.Join(backups, "expired.tar.gz"),
		filepath.Join(backups, "older-fresh.tar.gz"),
	} {
		if _, err := os.Stat(mustBeGone); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be deleted", mustBeGone)
		}
	}
}

func TestRetentionDisabledWhenZero(t *testing.T) {
	tmp := t.TempDir()
	outbox := filepath.Join(tmp, "outbox")
	_ = os.MkdirAll(outbox, 0o755)
	oldFile := filepath.Join(outbox, "keep.txt")
	_ = os.WriteFile(oldFile, []byte("keep"), 0o644)
	oldTime := time.Now().Add(-365 * 24 * time.Hour)
	_ = os.Chtimes(oldFile, oldTime, oldTime)

	cfg := config.GatewaySettings{
		RetentionDays:    0,
		BrainBackupMaxMB: 0,
		Dirs:             config.AppDirs{OutboxDir: outbox},
	}
	rep := RunRetentionCleanupWithBackupsDir(cfg, "", time.Now())
	if rep.OutboxRemoved != 0 {
		t.Fatalf("expected 0 removed when disabled, got %+v", rep)
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Fatalf("expected file to remain when retention disabled")
	}
}

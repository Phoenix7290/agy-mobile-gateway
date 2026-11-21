package automation

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Phoenix7290/agy-mobile-gateway/backend-go/internal/config"
)

// RetentionReport summarizes how many stale files and bytes were reclaimed by TTL cleanup.
type RetentionReport struct {
	InboxRemoved        int
	OutboxRemoved       int
	BackupsRemoved      int
	TotalBytesReclaimed int64
}

func resolveBrainBackupsDir() string {
	brainDir := strings.TrimSpace(os.Getenv("AGY_BRAIN_DIR"))
	if brainDir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		brainDir = filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	}
	return filepath.Join(filepath.Dir(filepath.Clean(brainDir)), "brain-backups")
}

// RunRetentionCleanup prunes stale inbox/outbox files and brain-backups archives based on configured TTL and size caps.
func RunRetentionCleanup(cfg config.GatewaySettings, now time.Time) RetentionReport {
	return RunRetentionCleanupWithBackupsDir(cfg, resolveBrainBackupsDir(), now)
}

// RunRetentionCleanupWithBackupsDir runs retention cleanup against an explicit brain-backups directory.
func RunRetentionCleanupWithBackupsDir(cfg config.GatewaySettings, backupsDir string, now time.Time) RetentionReport {
	var rep RetentionReport
	var cutoff time.Time
	if cfg.RetentionDays > 0 {
		cutoff = now.Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour)
		rep.InboxRemoved, rep.TotalBytesReclaimed = pruneDirByAge(cfg.Dirs.InboxDir, cutoff, false, rep.TotalBytesReclaimed)
		rep.OutboxRemoved, rep.TotalBytesReclaimed = pruneDirByAge(cfg.Dirs.OutboxDir, cutoff, false, rep.TotalBytesReclaimed)
		if backupsDir != "" {
			var removed int
			removed, rep.TotalBytesReclaimed = pruneDirByAge(backupsDir, cutoff, true, rep.TotalBytesReclaimed)
			rep.BackupsRemoved += removed
		}
	}

	if cfg.BrainBackupMaxMB > 0 && backupsDir != "" {
		maxBytes := int64(cfg.BrainBackupMaxMB) * 1024 * 1024
		removed, reclaimed := enforceBackupSizeCap(backupsDir, maxBytes)
		rep.BackupsRemoved += removed
		rep.TotalBytesReclaimed += reclaimed
	}
	return rep
}

func pruneDirByAge(dir string, cutoff time.Time, onlyArchives bool, reclaimed int64) (int, int64) {
	if strings.TrimSpace(dir) == "" {
		return 0, reclaimed
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, reclaimed
	}
	removed := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if onlyArchives && !strings.HasSuffix(strings.ToLower(name), ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			fullPath := filepath.Join(dir, name)
			if os.Remove(fullPath) == nil {
				removed++
				reclaimed += info.Size()
			}
		}
	}
	return removed, reclaimed
}

func enforceBackupSizeCap(dir string, maxBytes int64) (int, int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	type backupFile struct {
		path    string
		size    int64
		modTime time.Time
	}
	var files []backupFile
	var totalSize int64
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".tar.gz") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		totalSize += info.Size()
		files = append(files, backupFile{
			path:    filepath.Join(dir, name),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
	}
	if totalSize <= maxBytes {
		return 0, 0
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.Before(files[j].modTime)
	})

	removed := 0
	var reclaimed int64
	for _, f := range files {
		if totalSize <= maxBytes {
			break
		}
		if os.Remove(f.path) == nil {
			totalSize -= f.size
			reclaimed += f.size
			removed++
		}
	}
	return removed, reclaimed
}

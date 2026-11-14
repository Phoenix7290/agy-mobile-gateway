package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --- LocalRunner Inbox/Outbox & Telemetry ---

func (r *LocalRunner) SaveToInbox(_ context.Context, localTmpPath, cleanName string) (string, error) {
	if err := os.MkdirAll(r.cfg.Dirs.InboxDir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(r.cfg.Dirs.InboxDir, cleanName)
	src, err := os.Open(localTmpPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	dst, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return "", err
	}
	return dest, nil
}

func (r *LocalRunner) ListOutbox(_ context.Context) ([]OutboxFile, error) {
	if err := os.MkdirAll(r.cfg.Dirs.OutboxDir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(r.cfg.Dirs.OutboxDir)
	if err != nil {
		return nil, err
	}
	out := []OutboxFile{}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		out = append(out, OutboxFile{
			Name:  ent.Name(),
			Size:  info.Size(),
			Mtime: float64(info.ModTime().UnixNano()) / 1e9,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Mtime > out[j].Mtime })
	return out, nil
}

func (r *LocalRunner) OpenOutboxFile(_ context.Context, filename string) (io.ReadCloser, func() error, error) {
	p := filepath.Join(r.cfg.Dirs.OutboxDir, filename)
	f, err := os.Open(p)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Close, nil
}

func (r *LocalRunner) DeleteOutboxFile(_ context.Context, filename string) error {
	p := filepath.Join(r.cfg.Dirs.OutboxDir, filename)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (r *LocalRunner) GetTelemetry(ctx context.Context) TelemetryData {
	var data TelemetryData

	// 1. NVIDIA GPU (if nvidia-smi is available)
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(
		cctx,
		"nvidia-smi",
		"--query-gpu=name,memory.used,memory.total,temperature.gpu",
		"--format=csv,noheader,nounits",
	).Output(); err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), ", ")
		if len(parts) >= 4 {
			used, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			tot, _ := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
			temp, _ := strconv.ParseFloat(strings.TrimSpace(parts[3]), 64)
			data.GPU = &GPUInfo{
				Name:        strings.TrimSpace(parts[0]),
				VRAMUsedMB:  int(math.Round(used)),
				VRAMTotalMB: int(math.Round(tot)),
				TempC:       int(math.Round(temp)),
			}
		}
	}

	// 2. RAM from /proc/meminfo (Linux)
	if f, err := os.Open("/proc/meminfo"); err == nil {
		var totalKB, availKB int
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			k, v, ok := strings.Cut(scanner.Text(), ":")
			if !ok {
				continue
			}
			fields := strings.Fields(v)
			if len(fields) == 0 {
				continue
			}
			n, _ := strconv.Atoi(fields[0])
			switch k {
			case "MemTotal":
				totalKB = n
			case "MemAvailable":
				availKB = n
			}
		}
		_ = f.Close()
		if totalKB > 0 {
			totMB := totalKB / 1024
			availMB := availKB / 1024
			data.RAM = &RAMInfo{UsedMB: totMB - availMB, TotalMB: totMB}
		}
	}

	// 3. CPU thermal sensors (Linux hwmon)
	if paths, err := filepath.Glob("/sys/class/hwmon/hwmon*/temp1_input"); err == nil {
		maxC := -1.0
		for i, p := range paths {
			if i >= 4 {
				break
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			milli, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				continue
			}
			c := float64(milli) / 1000.0
			if c > maxC {
				maxC = c
			}
		}
		if maxC >= 0 {
			rounded := math.Round(maxC*10) / 10
			data.CPUTemp = &rounded
		}
	}

	return data
}

// --- SSHRunner Inbox/Outbox & Telemetry ---

func (r *SSHRunner) SaveToInbox(ctx context.Context, localTmpPath, cleanName string) (string, error) {
	remotePath := fmt.Sprintf("%s/%s", strings.TrimRight(r.cfg.Dirs.InboxDir, "/"), cleanName)
	scpArgs := []string{
		"-i", r.cfg.SSHKey,
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "StrictHostKeyChecking=no",
		"-o", "BatchMode=yes",
		localTmpPath,
		fmt.Sprintf("%s@%s:%s", r.cfg.TargetUser, r.cfg.TargetHost, remotePath),
	}
	cmd := exec.CommandContext(ctx, "scp", scpArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("SCP falhou: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return remotePath, nil
}

func (r *SSHRunner) ListOutbox(ctx context.Context) ([]OutboxFile, error) {
	pyCmd := fmt.Sprintf(
		`python3 -c 'import os,json; p=%s; os.makedirs(p,exist_ok=True); print(json.dumps([{"name":f,"size":os.path.getsize(os.path.join(p,f)),"mtime":os.path.getmtime(os.path.join(p,f))} for f in sorted(os.listdir(p)) if os.path.isfile(os.path.join(p,f))]))'`,
		shellQuote(r.cfg.Dirs.OutboxDir),
	)
	stdout, err := r.runSSH(ctx, pyCmd, 4*time.Second)
	if err != nil || stdout == "" {
		return []OutboxFile{}, nil
	}
	var files []OutboxFile
	if json.Unmarshal([]byte(stdout), &files) != nil {
		return []OutboxFile{}, nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Mtime > files[j].Mtime })
	return files, nil
}

func (r *SSHRunner) OpenOutboxFile(ctx context.Context, filename string) (io.ReadCloser, func() error, error) {
	remotePath := fmt.Sprintf("%s/%s", strings.TrimRight(r.cfg.Dirs.OutboxDir, "/"), filename)
	args := append(r.sshBaseArgs(5), "cat "+shellQuote(remotePath))
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

func (r *SSHRunner) DeleteOutboxFile(ctx context.Context, filename string) error {
	remotePath := fmt.Sprintf("%s/%s", strings.TrimRight(r.cfg.Dirs.OutboxDir, "/"), filename)
	_, err := r.runSSH(ctx, "rm -f "+shellQuote(remotePath), 4*time.Second)
	return err
}

func (r *SSHRunner) GetTelemetry(ctx context.Context) TelemetryData {
	const pyCmd = `python3 -c 'import json,subprocess,glob
res={"gpu":None,"cpu_temp":None,"ram":None}
try:
 o=subprocess.check_output(["nvidia-smi","--query-gpu=name,memory.used,memory.total,temperature.gpu","--format=csv,noheader,nounits"],timeout=2).decode().strip().split(", ")
 res["gpu"]={"name":o[0],"vram_used_mb":round(float(o[1])),"vram_total_mb":round(float(o[2])),"temp_c":round(float(o[3]))}
except Exception: pass
try:
 m={k:int(v.split()[0]) for k,v in (l.split(":") for l in open("/proc/meminfo") if ":" in l)}
 tot=m.get("MemTotal",0)//1024; avail=m.get("MemAvailable",0)//1024
 res["ram"]={"used_mb":tot-avail,"total_mb":tot}
except Exception: pass
try:
 temps=[int(open(p).read().strip())/1000 for p in glob.glob("/sys/class/hwmon/hwmon*/temp1_input")[:4]]
 if temps: res["cpu_temp"]=round(max(temps),1)
except Exception: pass
print(json.dumps(res))'`

	stdout, err := r.runSSH(ctx, pyCmd, 4*time.Second)
	if err != nil || stdout == "" {
		return TelemetryData{}
	}
	var data TelemetryData
	_ = json.Unmarshal([]byte(stdout), &data)
	return data
}

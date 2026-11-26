package runner

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// workspaceCleanupLoop 定期清理开发机上过期的运行工作区，避免每个流程实例
// 都在本机累积产物与仓库副本。默认保留 7 天（AGENTFLOW_RUNNER_WORKSPACE_TTL_HOURS，
// 0 表示关闭）。
func (d *Daemon) workspaceCleanupLoop(ctx context.Context) {
	ttl := d.cfg.WorkspaceTTL
	if ttl <= 0 {
		return
	}
	// 启动后错开一段时间，避免与首次任务准备争用磁盘。
	timer := time.NewTimer(3 * time.Minute)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		d.sweepRunWorkspaces(ctx, ttl)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) sweepRunWorkspaces(ctx context.Context, ttl time.Duration) {
	root, err := filepath.Abs(d.cfg.DefaultWorkspaceRoot)
	if err != nil {
		return
	}
	runsDir := filepath.Join(root, "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return
	}
	now := time.Now()
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runID := entry.Name()
		if runID == "" || strings.HasPrefix(runID, ".") {
			continue
		}
		if d.runWorkspaceActive(runID) {
			continue
		}
		target := filepath.Join(runsDir, runID)
		newest := newestWorkspaceMtime(target)
		if newest.IsZero() || now.Sub(newest) < ttl {
			continue
		}
		d.closeRunSandboxes(ctx, runID)
		_ = cleanupDockerWorkspace(ctx, target)
		if err := os.RemoveAll(target); err != nil {
			log.Printf("runner workspace cleanup %s: %v", runID, err)
			continue
		}
		removed++
		log.Printf("runner workspace cleanup removed %s (idle %s)", runID, now.Sub(newest).Truncate(time.Hour))
	}
	if removed > 0 {
		log.Printf("runner workspace cleanup: removed %d idle run workspace(s)", removed)
	}
}

// runWorkspaceActive 判断该运行当前是否仍有活动沙箱或正在准备工作区。
func (d *Daemon) runWorkspaceActive(runID string) bool {
	d.runSandboxMu.Lock()
	for _, entry := range d.runSandboxes {
		if entry != nil && entry.runID == runID {
			d.runSandboxMu.Unlock()
			return true
		}
	}
	d.runSandboxMu.Unlock()
	active := false
	d.workspacePreparing.Range(func(key, _ any) bool {
		if name, ok := key.(string); ok && (name == runID || strings.HasPrefix(name, runID+"/") || strings.HasPrefix(name, runID+"-")) {
			active = true
			return false
		}
		return true
	})
	if active {
		return true
	}
	d.workspacePreparation.Range(func(key, _ any) bool {
		if name, ok := key.(string); ok && (name == runID || strings.HasPrefix(name, runID+"/") || strings.HasPrefix(name, runID+"-")) {
			active = true
			return false
		}
		return true
	})
	return active
}

// newestWorkspaceMtime 用运行目录、workspace 与 .git 的修改时间近似最近活动时间，
// 避免对大型仓库做全量遍历。
func newestWorkspaceMtime(runDir string) time.Time {
	var newest time.Time
	for _, path := range []string{
		runDir,
		filepath.Join(runDir, "workspace"),
		filepath.Join(runDir, "workspace", ".git"),
	} {
		if info, err := os.Stat(path); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

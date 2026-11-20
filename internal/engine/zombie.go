package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"agentflow/internal/domain"
)

// zombieTimeout 是判定「僵尸运行」的阈值：运行状态是 running，但超过这个
// 时间没有任何状态更新，说明执行侧已经不在动（进程丢失、写库失败等）。
// 可用 AGENTFLOW_ZOMBIE_TIMEOUT_MIN 覆盖，单位分钟，默认 20。
func zombieTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("AGENTFLOW_ZOMBIE_TIMEOUT_MIN"))
	if raw == "" {
		return 20 * time.Minute
	}
	if raw == "0" || strings.EqualFold(raw, "off") {
		return 0 // 关闭僵尸检测
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 20 * time.Minute
	}
	return time.Duration(n) * time.Minute
}

func (e *Engine) zombieWatch(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.detectZombieRuns()
		}
	}
}

// detectZombieRuns 把长时间没有状态更新的 running 运行标记为失败，
// 避免「界面显示在跑、实际已经停了」这种静默挂死。
func (e *Engine) detectZombieRuns() {
	timeout := zombieTimeout()
	if timeout <= 0 {
		return
	}
	now := time.Now().UTC()
	for _, summary := range e.repo.Runs() {
		if summary.Status != "running" {
			continue
		}
		if summary.CancelRequested || summary.PauseRequested {
			continue
		}
		// 只针对「无人值守的全自动运行」：交互式工作区、群组协作、
		// 以及任何等待人工输入的节点都不算僵尸——它们在等人，不是卡死。
		if summary.Kind == domain.RunKindWorkspace {
			continue
		}
		if summary.WorkflowSnapshot.CollaborationMode == "group" {
			continue
		}
		last := summary.UpdatedAt
		if last.IsZero() || now.Sub(last) < timeout {
			continue
		}
		runID := summary.ID
		run, ok := e.repo.RunState(runID)
		if !ok || run.Status != "running" {
			continue
		}
		interactive := false
		// 交互节点（终端接管）在等人，可能几十分钟到几小时没有状态写入，
		// 不能按“无更新”判僵尸；会话有 48 小时上限由 Runner 自己兜底。
		interactiveNodes := map[string]bool{}
		for _, cfg := range run.WorkflowSnapshot.Nodes {
			if cfg.Interactive {
				interactiveNodes[cfg.ID] = true
			}
		}
		for id, node := range run.Nodes {
			if node == nil {
				continue
			}
			if node.Status == "pending_input" || node.Status == "waiting_approval" {
				interactive = true
				break
			}
			if node.Status == "running" && interactiveNodes[id] {
				interactive = true
				break
			}
		}
		if interactive {
			continue
		}
		note := fmt.Sprintf("运行超过 %s 没有状态更新，判定为僵尸运行并终止（最后更新 %s，请检查执行机器与平台日志）",
			timeout, last.UTC().Format(time.RFC3339))
		affected := map[string]bool{}
		for id, node := range run.Nodes {
			if node != nil && (node.Status == "running" || node.Status == "pending_input") {
				affected[id] = true
			}
		}
		marked := e.updateRunAt(runID, false, func(r *domain.Run) {
			if r.Status != "running" {
				return
			}
			for _, node := range r.Nodes {
				if node != nil && (node.Status == "running" || node.Status == "pending_input") {
					node.Status = "failed"
					node.Error = note
					node.FinishedAt = now
					node.Activity = ""
				}
			}
			r.Status = "failed"
			e.event(r, "", "run.zombie", note)
		})
		if !marked {
			log.Printf("zombie run %s: mark failed 失败（状态未持久化），等待下次扫描", runID)
			continue
		}
		log.Printf("zombie run %s: %s", runID, note)
		if len(affected) > 0 {
			e.cancelNodeExecutions(runID, affected)
		}
	}
}

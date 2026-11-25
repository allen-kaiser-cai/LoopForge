package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
)

const (
	ideRemindAfter = 24 * time.Hour
	ideFailAfter   = 5 * 24 * time.Hour
	idePollEvery   = 5 * time.Second
)

// runIDEInteractive 在 IDE 模式下等待开发者在自己的 IDE 中完成交互节点：
// 平台创建任务，开发通过 MCP/API 上传产物并申请提交，责任人钉钉确认后节点继续。
func (e *Engine) runIDEInteractive(ctx context.Context, run domain.Run, node domain.WorkflowNode, workspace, gatePath string, sink runtime.EventSink) (string, error) {
	userID := run.ExecutionContext.ResponsibleUserID
	branch := ""
	if repos := run.RepositoryContexts(); len(repos) > 0 {
		branch = strings.TrimSpace(repos[0].WorkBranch)
	}
	taskID := ideTaskID(run.ID, node.ID)
	task := &repo.IDETask{
		ID: taskID, RunID: run.ID, NodeID: node.ID,
		ProjectID: run.ExecutionContext.ProjectID, IssueID: run.ExecutionContext.IssueID,
		UserID: userID, Status: "waiting", BranchName: branch, Artifacts: json.RawMessage("{}"),
	}
	if err := e.repo.CreateIDETask(*task); err != nil {
		return "", fmt.Errorf("create ide task: %w", err)
	}
	if sink != nil {
		sink(domain.TelemetryEvent{Category: "sandbox", Name: "ide.waiting", Phase: "progress",
			Message: "等待开发在 IDE 中完成：平台已派发交互任务，可在 Qoder 等 IDE 中通过 MCP 拉取"})
	}
	started := time.Now()
	reminded := false
	ticker := time.NewTicker(idePollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = e.repo.MarkIDETaskStatus(taskID, "canceled")
			return "", ctx.Err()
		case <-ticker.C:
		}
		t, found := e.repo.IDETask(taskID)
		if !found {
			return "", fmt.Errorf("ide task disappeared")
		}
		switch t.Status {
		case "confirmed":
			if err := e.applyIDEArtifacts(workspace, node, &t); err != nil {
				return "", err
			}
			decision := strings.ToLower(strings.TrimSpace(t.Decision))
			if decision == "" {
				decision = "pass"
			}
			reason := strings.TrimSpace(t.Note)
			if reason == "" {
				reason = "开发在 IDE 中提交"
			}
			if path := strings.TrimSpace(gatePath); path != "" {
				gate := map[string]any{"decision": decision, "reason": reason, "summary": reason, "findings": []any{}}
				if data, err := json.MarshalIndent(gate, "", "  "); err == nil {
					target := filepath.Join(workspace, filepath.FromSlash(path))
					_ = os.MkdirAll(filepath.Dir(target), 0o755)
					_ = os.WriteFile(target, data, 0o644)
				}
			}
			if sink != nil {
				sink(domain.TelemetryEvent{Category: "process", Name: "ide.submitted", Phase: "finish",
					Message: "收到 IDE 提交：" + decision + "（" + reason + "）"})
			}
			return "IDE 提交：" + decision + "（" + reason + "）", nil
		case "expired", "failed":
			return "", fmt.Errorf("IDE 交互任务已超时或失败")
		}
		if !reminded && time.Since(started) >= ideRemindAfter {
			reminded = true
			if sink != nil {
				sink(domain.TelemetryEvent{Category: "verification", Name: "ide.remind", Phase: "progress", Level: "warn",
					Message: "IDE 交互任务已超过 1 天未提交，请提醒责任人尽快完成"})
			}
		}
		if time.Since(started) >= ideFailAfter {
			_ = e.repo.MarkIDETaskStatus(taskID, "expired")
			return "", fmt.Errorf("IDE 交互任务超过 5 天未提交，已判定失败")
		}
	}
}

// applyIDEArtifacts 把开发者上传的产物写入运行工作区，供下游节点与产物契约使用。
func (e *Engine) applyIDEArtifacts(workspace string, node domain.WorkflowNode, t *repo.IDETask) error {
	arts := t.ArtifactList()
	paths := map[string]string{}
	for _, c := range node.ArtifactOutputs {
		paths[c.Name] = c.Path
	}
	for name, content := range arts {
		rel := paths[name]
		if rel == "" {
			rel = name
		}
		target := filepath.Join(workspace, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ideTaskID 与 runIDEInteractive 中创建任务时的 ID 规则保持一致。
func ideTaskID(runID, nodeID string) string {
	return "ide-" + runID + "-" + nodeID
}

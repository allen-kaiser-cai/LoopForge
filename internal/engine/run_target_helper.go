package engine

import (
	"fmt"
	"strings"

	"agentflow/internal/domain"
)

// applyWorkflowExecutionTarget 应用流程级执行目标配置：
// personal 固定落在责任人默认执行机；cloud 落在云池；空值沿用上游默认。
func (e *Engine) applyWorkflowExecutionTarget(wf domain.WorkflowDefinition, executionContext *domain.TelemetryContext) error {
	if executionContext.ParentRunID != "" || executionContext.ExecutionPinned {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(wf.ExecutionTarget)) {
	case "personal":
		executionContext.ExecutionPool = ""
		executionContext.ExecutionRunnerID = e.repo.DefaultRunner(executionContext.ResponsibleUserID)
		if executionContext.ExecutionRunnerID == "" {
			return fmt.Errorf("该流程要求在人机协同模式下执行，但责任人（用户 %d）还没有绑定默认执行机，请先在执行机器页面绑定", executionContext.ResponsibleUserID)
		}
		executionContext.ExecutionMode = domain.ExecutionModeRunner
		executionContext.RunnerWorkspaceLayout = domain.RunnerWorkspaceLayoutShared
	case "cloud":
		executionContext.ExecutionPool = domain.ExecutionModeCloud
		if runner, ok := e.cloudPoolRunner(executionContext.ProjectID); ok {
			executionContext.ExecutionRunnerID = runner.ID
		}
		executionContext.ExecutionMode = domain.ExecutionModeRunner
		executionContext.RunnerWorkspaceLayout = domain.RunnerWorkspaceLayoutShared
	}
	return nil
}

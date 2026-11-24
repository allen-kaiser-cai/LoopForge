package engine

import (
	"log"
	"strings"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
)

// executorFallbackChain 是偏好不可用时依次尝试的智能体（按要求固定顺序）。
var executorFallbackChain = []string{"codebuddy", "pi", "dsh", "codex"}

// resolveExecutorForRun 在个人开发机场景下按“账号偏好 → 回退链 → 标准执行器”
// 选择实际使用的智能体；云池运行保持流程标准执行器不变。
func (e *Engine) resolveExecutorForRun(run domain.Run, fallback domain.ExecutorDefinition) domain.ExecutorDefinition {
	if !strings.EqualFold(strings.TrimSpace(run.WorkflowSnapshot.ExecutionTarget), "personal") {
		return fallback
	}
	userID := run.ExecutionContext.ResponsibleUserID
	preferred := strings.TrimSpace(e.repo.PreferredExecutorID(userID))
	if preferred == "" {
		return fallback
	}
	runnerID := strings.TrimSpace(run.ExecutionContext.ExecutionRunnerID)
	runner, ok := e.repo.Runner(runnerID)
	if !ok {
		return fallback
	}
	candidates := append([]string{preferred}, executorFallbackChain...)
	seen := map[string]bool{}
	for _, id := range candidates {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		def, ok := repo.BuiltInExecutor(id)
		if !ok {
			continue
		}
		if !strings.EqualFold(def.Kind, fallback.Kind) && !runner.SupportsProvider(def.Kind) {
			continue
		}
		// 平台凭据模式下，账号未配置该 CLI 的 Key 时视为不可用，继续回退。
		if strings.EqualFold(runner.RuntimeAuthMode(def.Kind), string(domain.RuntimeAuthPersonal)) {
			if user, ok := e.repo.User(userID); !ok || !userHasAgentCredential(user, def.Kind) {
				continue
			}
		}
		if !strings.EqualFold(def.Kind, fallback.Kind) {
			log.Printf("executor: user %d prefers %s，回退到 %s（%s）", userID, preferred, def.ID, def.Kind)
		}
		return def
	}
	return fallback
}

// userHasAgentCredential 判断账号是否具备某个执行器 kind 的平台凭据。
func userHasAgentCredential(user domain.User, kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "claude-code", "claude":
		return strings.TrimSpace(user.ClaudeCodeAPIKey) != ""
	case "codex":
		return strings.TrimSpace(user.CodexAPIKey) != ""
	}
	cred, ok := user.AgentCLICredentials[kind]
	return ok && strings.TrimSpace(cred.APIKey) != ""
}

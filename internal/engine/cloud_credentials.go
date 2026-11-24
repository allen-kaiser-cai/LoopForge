package engine

import (
	"context"
	"os"
	"strconv"
	"strings"

	"agentflow/internal/domain"
)

// cloudServiceUserID 是云池运行统一使用的服务账号（默认管理员）。
// 云池是共享执行机，不应要求每个责任人各自配置 Git/模型凭据。
func cloudServiceUserID() int64 {
	if v := strings.TrimSpace(os.Getenv("AGENTFLOW_SERVICE_USER_ID")); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	return 1
}

func cloudPoolRunnerLabel() string {
	if v := strings.TrimSpace(os.Getenv("AGENTFLOW_CLOUD_RUNNER_LABEL")); v != "" {
		return v
	}
	return "lf-pool"
}

// isCloudPoolRunner 判断执行机是否为云池共享执行机。
func (e *Engine) isCloudPoolRunner(runner domain.Runner) bool {
	want := cloudPoolRunnerLabel()
	for _, label := range runner.Labels {
		if strings.TrimSpace(label) == want {
			return true
		}
	}
	return false
}

// isCloudPoolRun 判断运行是否落在云池上。
func (e *Engine) isCloudPoolRun(run domain.Run) bool {
	if id := strings.TrimSpace(run.ExecutionContext.ExecutionRunnerID); id != "" {
		if runner, ok := e.repo.Runner(id); ok {
			return e.isCloudPoolRunner(runner)
		}
		return false
	}
	return run.ExecutionContext.ExecutionMode == domain.ExecutionModeCloud
}

// platformGitToken 返回平台级共享 Git 令牌：优先环境变量，其次服务账号。
// 开发者个人未配置令牌时回退到这里，避免逐个给同事配置。
func (e *Engine) platformGitToken(ctx context.Context) string {
	if v := strings.TrimSpace(os.Getenv("AGENTFLOW_PLATFORM_GIT_TOKEN")); v != "" {
		return v
	}
	return strings.TrimSpace(resolveGitToken(e.repo, cloudServiceUserID()))
}

package engine

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"agentflow/internal/domain"
	"agentflow/internal/runtime"
)

// ideBranchCheck 在 IDE 模式下代替 Runner 的 push_branch 节点：
// 开发已在本地推送分支时直接通过；未推送则快速失败并给出明确指引，避免任务挂在无执行机的队列里。
func (e *Engine) ideBranchCheck(ctx context.Context, run domain.Run, gitTok string, sink runtime.EventSink) (string, error) {
	missing := []string{}
	checked := 0
	for _, rc := range run.RepositoryContexts() {
		branch := strings.TrimSpace(rc.WorkBranch)
		if branch == "" || strings.TrimSpace(rc.CloneURL) == "" {
			continue
		}
		checked++
		if !remoteBranchExists(ctx, rc.CloneURL, branch, gitTok) {
			missing = append(missing, fmt.Sprintf("%s@%s", rc.Name, branch))
		}
	}
	if checked == 0 {
		return "没有需要推送的分支，跳过", nil
	}
	if len(missing) > 0 {
		if sink != nil {
			sink(domain.TelemetryEvent{Category: "verification", Name: "ide.branch.missing", Phase: "finish", Level: "warn",
				Message: "以下分支尚未推送到远端：" + strings.Join(missing, ", ")})
		}
		return "", fmt.Errorf("以下分支尚未推送到远端：%s。请在本地执行 git push origin <分支名> 后重试本节点", strings.Join(missing, "，"))
	}
	if sink != nil {
		sink(domain.TelemetryEvent{Category: "verification", Name: "ide.branch.ok", Phase: "finish",
			Message: "分支已推送到远端，跳过平台推送"})
	}
	return "分支已推送到远端，跳过平台推送", nil
}

func remoteBranchExists(ctx context.Context, cloneURL, branch, token string) bool {
	url := withTokenURL(cloneURL, token)
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", url, branch)
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "refs/heads/"+branch)
}

func withTokenURL(cloneURL, token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return cloneURL
	}
	if strings.HasPrefix(cloneURL, "https://") {
		return "https://oauth2:" + token + "@" + strings.TrimPrefix(cloneURL, "https://")
	}
	return cloneURL
}

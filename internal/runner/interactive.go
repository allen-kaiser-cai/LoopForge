package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentflow/internal/domain"
)

// interactiveSubmit 是终端接管模式下人工提交的决策文件内容。
type interactiveSubmit struct {
	Decision string `json:"decision"`
	Note     string `json:"note,omitempty"`
	// Target 为 rework 时的改派目标（frontend 等）。
	Target string `json:"target,omitempty"`
	// Assignee 为 rework 时指定的改派账号（login），优先于 Target。
	Assignee string `json:"assignee,omitempty"`
}

// executeInteractiveInvocation 在 tmux 会话里以交互方式启动 Agent CLI，
// 由人工在会话里与 Agent 协作，完成后用 `lf-session submit` 写入决策，
// 这里负责写终态（gate result）、收集会话日志并结束会话。
func (d *Daemon) executeInteractiveInvocation(ctx context.Context, task domain.RunnerTask, workspace string, argv []string, env map[string]string) execResult {
	inv := task.Invocation
	if inv == nil {
		return execResult{status: domain.RunnerTaskFailed, errMsg: "interactive invocation missing"}
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return execResult{status: domain.RunnerTaskFailed, errMsg: "本机未安装 tmux，终端接管模式需要 tmux（macOS: brew install tmux / Linux: apt install tmux）"}
	}
	dir := filepath.Join(workspace, ".agentflow", "interactive")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return execResult{status: domain.RunnerTaskFailed, errMsg: fmt.Sprintf("create interactive dir: %v", err)}
	}
	prompt, interactiveArgv := splitInteractiveArgv(argv)
	// 把宿主机 ~/.claude/settings.json 带进私有配置目录：
	// 否则 CLAUDE_CONFIG_DIR 指向空目录时，CLI 会走默认联网检查并连 api.anthropic.com。
	prepareInteractiveClaudeConfig(workspace, env)
	acceptInteractiveClaudePrompts(workspace, env)
	prompt = prompt + "\n\n## 工作区说明\n\n本交互会话**未预克隆任何代码仓库**。请根据领域知识判断涉及哪些微服务，自行用 git clone 拉取需要的仓库（Git 凭据已注入会话环境，可直接 clone 内网 GitLab 地址）。完成时平台会扫描工作区里所有 git 仓库并生成变更报告。\n"
	if err := os.WriteFile(filepath.Join(dir, "task.md"), []byte(prompt), 0o644); err != nil {
		return execResult{status: domain.RunnerTaskFailed, errMsg: fmt.Sprintf("write task prompt: %v", err)}
	}
	startScript := filepath.Join(dir, "start.sh")
	var b strings.Builder
	b.WriteString("#!/bin/bash\nset -u\ncd " + shellQuote(workspace) + "\n")
	// tmux server 可能由别的会话创建，new-session 会继承旧环境；
	// 这里把模型/仓库凭据显式写进启动脚本，保证会话内一定拿得到。
	for _, k := range interactiveEnvKeys(env) {
		b.WriteString("export " + k + "=" + shellQuote(env[k]) + "\n")
	}
	b.WriteString("export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1\n")
	b.WriteString("exec " + shellQuote(interactiveArgv[0]))
	for _, a := range interactiveArgv[1:] {
		b.WriteString(" " + shellQuote(a))
	}
	b.WriteString("\n")
	if err := os.WriteFile(startScript, []byte(b.String()), 0o755); err != nil {
		return execResult{status: domain.RunnerTaskFailed, errMsg: fmt.Sprintf("write start script: %v", err)}
	}
	session := interactiveSessionName(task.RunID, task.NodeID)
	logPath := filepath.Join(dir, "session.log")
	_ = os.Remove(logPath)
	_ = os.Remove(filepath.Join(dir, "submit.json"))
	_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	start := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", workspace, "bash "+shellQuote(startScript))
	start.Env = envSlice(env)
	if out, err := start.CombinedOutput(); err != nil {
		return execResult{status: domain.RunnerTaskFailed, errMsg: fmt.Sprintf("tmux start: %v %s", err, strings.TrimSpace(string(out)))}
	}
	_ = exec.Command("tmux", "pipe-pane", "-t", session, "-o", "cat >> "+shellQuote(logPath)).Run()
	// 会话已就绪：立即上报一次，让平台把“准备工作区/沙箱”更新为“等待人工操作”。
	readyBatcher := newEventBatcher(task, d.client)
	readyBatcher.addObservation(domain.TelemetryEvent{
		Category: "process", Name: "interactive.ready", Phase: "progress", Level: "info",
		Message: "交互会话已就绪，等待人工操作：tmux attach -t " + session,
	})
	readyBatcher.flush(ctx)
	// 等待人工提交：24 小时无动作仅记录提醒，48 小时自动失败。
	submitPath := filepath.Join(dir, "submit.json")
	deadline := time.Now().Add(48 * time.Hour)
	// 交互等待期间必须持续上报心跳：否则平台会按“运行长时间无更新”
	// 把会话当成僵尸运行杀掉（实测 20 分钟即误杀）。
	batcher := newEventBatcher(task, d.client)
	lastBeat := time.Now()
	// 会话提前退出（例如 CLI 启动失败）时，不要干等 48 小时：
	// 会话消失超过 5 分钟仍无提交，按失败处理并带上日志尾部。
	sessionGoneAt := time.Time{}
	submitted := (*interactiveSubmit)(nil)
	for {
		if ctx.Err() != nil {
			_ = exec.Command("tmux", "kill-session", "-t", session).Run()
			return execResult{status: domain.RunnerTaskCanceled, errMsg: "task canceled"}
		}
		if data, err := os.ReadFile(submitPath); err == nil {
			var sub interactiveSubmit
			if json.Unmarshal(data, &sub) == nil && strings.TrimSpace(sub.Decision) != "" {
				submitted = &sub
				break
			}
		}
		if time.Now().After(deadline) {
			_ = exec.Command("tmux", "kill-session", "-t", session).Run()
			return execResult{status: domain.RunnerTaskFailed, errMsg: "交互会话超过 48 小时未提交，已自动挂起"}
		}
		if exec.Command("tmux", "has-session", "-t", session).Run() != nil {
			if sessionGoneAt.IsZero() {
				sessionGoneAt = time.Now()
				batcher.addObservation(domain.TelemetryEvent{
					Category: "process", Name: "interactive.detached", Phase: "progress", Level: "info",
					Message: "交互会话已退出，仍在等待人工提交（开发者可稍后继续）",
				})
				batcher.flush(ctx)
			}
			if time.Since(sessionGoneAt) > 12*time.Hour {
				tail := tailFile(filepath.Join(dir, "session.log"), 2000)
				return execResult{status: domain.RunnerTaskFailed, errMsg: "交互会话退出后 12 小时仍未提交。会话日志尾部：\n" + tail}
			}
		} else {
			sessionGoneAt = time.Time{}
		}
		if time.Since(lastBeat) >= 30*time.Second {
			// 续租 + 事件双通道：续租保证平台租约不过期，
			// 事件保证平台节点活动状态实时刷新。
			if _, err := d.client.RenewLease(ctx, task.ID); err != nil {
				_ = err
			}
			batcher.addObservation(domain.TelemetryEvent{
				Category: "process", Name: "interactive.waiting", Phase: "progress", Level: "info",
				Message: "交互会话进行中：tmux attach -t " + session + "（等待人工提交）",
			})
			batcher.flush(ctx)
			lastBeat = time.Now()
		}
		time.Sleep(2 * time.Second)
	}
	_ = exec.Command("tmux", "pipe-pane", "-t", session, "-o", "").Run()
	_ = exec.Command("tmux", "kill-session", "-t", session).Run()
	writeInteractiveRepoReport(workspace)
	// 会话日志落盘为 session-log.md（超过 5MB 截断）
	copyTruncatedLog(logPath, filepath.Join(workspace, "session-log.md"), 5<<20)
	// 写终态给引擎：pass 继续下游，rework 打回上游
	decision := strings.ToLower(strings.TrimSpace(submitted.Decision))
	if decision != "pass" && decision != "rework" {
		decision = "rework"
	}
	reason := strings.TrimSpace(submitted.Note)
	if reason == "" {
		reason = "人工在交互会话中提交"
	}
	gate := map[string]any{"decision": decision, "reason": reason, "summary": reason, "findings": []any{}}
	if assignee := strings.TrimSpace(submitted.Assignee); assignee != "" {
		gate["assignee"] = assignee
		reason = strings.TrimSpace(reason) + " [改派账号:" + assignee + "]"
	} else if target := strings.ToLower(strings.TrimSpace(submitted.Target)); target != "" {
		gate["target"] = target
		reason = strings.TrimSpace(reason) + " [改派:" + target + "]"
	}
	if path := strings.TrimSpace(inv.GateResultPath); path != "" {
		target := path
		if !filepath.IsAbs(target) {
			target = filepath.Join(workspace, filepath.FromSlash(path))
		}
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		if data, err := json.MarshalIndent(gate, "", "  "); err == nil {
			_ = os.WriteFile(target, data, 0o644)
		}
	}
	if decision == "rework" {
		marker := ""
		if assignee := strings.TrimSpace(submitted.Assignee); assignee != "" {
			marker = " REASSIGN:user:" + assignee
		} else if target := strings.ToLower(strings.TrimSpace(submitted.Target)); target != "" {
			marker = " REASSIGN:" + target
		}
		return execResult{status: domain.RunnerTaskSuccess, exitCode: 0, result: "人工提交：需要返工（" + reason + "）" + marker}
	}
	return execResult{status: domain.RunnerTaskSuccess, exitCode: 0, result: "人工提交：通过（" + reason + "）"}
}

func interactiveSessionName(runID, nodeID string) string {
	name := "lf-" + runID + "-" + nodeID
	name = strings.ReplaceAll(name, " ", "-")
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

// splitInteractiveArgv 从非交互 argv 里取出任务提示词，并构造交互式 argv：
// 去掉 -p / --output-format 等一次性参数，改为把任务书指令作为开场消息。
func splitInteractiveArgv(argv []string) (string, []string) {
	if len(argv) == 0 {
		return "", []string{"bash"}
	}
	prompt := argv[len(argv)-1]
	out := make([]string, 0, len(argv))
	for i := 0; i < len(argv)-1; i++ {
		a := argv[i]
		switch a {
		case "-p", "--print":
			continue
		case "--output-format":
			i++
			continue
		case "--input-format":
			i++
			continue
		case "--mode":
			// Pi 的 --mode json 是无人值守输出模式，交互会话要去掉。
			i++
			continue
		case "--session-id":
			// 交互会话使用 tmux 里的原生会话，不再复用 headless 会话 ID。
			i++
			continue
		}
		if strings.HasPrefix(a, "--output-format=") || strings.HasPrefix(a, "--input-format=") {
			continue
		}
		out = append(out, a)
	}
	if len(out) == 0 {
		out = []string{"claude"}
	}
	// Pi 的原始 argv 没有子命令（pi --model ...），去掉 headless 参数后即可交互运行。
	out = append(out, "请先阅读 .agentflow/interactive/task.md 了解任务背景。在我说「开始」之前，不要调用任何工具、不要修改文件，只回复你已了解任务并等待我的指示。")
	return prompt, out
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func envSlice(env map[string]string) []string {
	out := os.Environ()
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	// 稳定顺序，便于排查
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func copyTruncatedLog(src, dst string, limit int64) {
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if int64(len(data)) > limit {
		data = append(data[:limit], []byte("\n\n> 会话日志超过 5MB，已截断。\n")...)
	}
	_ = os.WriteFile(dst, data, 0o644)
}

// interactiveEnvKeys 选出需要带进交互会话的环境变量（模型凭据与仓库访问）。
func interactiveEnvKeys(env map[string]string) []string {
	prefixes := []string{"ANTHROPIC_", "OPENAI_", "CODEX_", "CLAUDE_", "AGENTFLOW_", "GIT_", "PATH",
		"PI_", "DEEPSEEK_", "KIMI_", "OPENCODE_", "CURSOR_", "AGY_", "ZCODE_", "DSH_"}
	out := make([]string, 0, len(env))
	for k, v := range env {
		if strings.TrimSpace(v) == "" {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(k, p) {
				out = append(out, k)
				break
			}
		}
	}
	// 稳定顺序
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func tailFile(path string, limit int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "（无会话日志）"
	}
	s := strings.ToValidUTF8(string(data), "")
	if len(s) > limit {
		s = s[len(s)-limit:]
	}
	return strings.TrimSpace(s)
}

// prepareInteractiveClaudeConfig 把宿主机 Claude 配置复制到私有配置目录，
// 保证交互会话里的 CLI 能拿到与终端一致的环境（模型端点、禁用联网检查等）。
func prepareInteractiveClaudeConfig(workspace string, env map[string]string) {
	cfgDir := strings.TrimSpace(env["CLAUDE_CONFIG_DIR"])
	if cfgDir == "" {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	src := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	target := filepath.Join(cfgDir, "settings.json")
	if _, err := os.Stat(target); err == nil {
		return
	}
	if os.MkdirAll(cfgDir, 0o700) != nil {
		return
	}
	_ = os.WriteFile(target, data, 0o600)
}

// acceptInteractiveClaudePrompts 预先接受信任目录与危险模式确认，
// 避免每个会话重复弹三个选择框。
func acceptInteractiveClaudePrompts(workspace string, env map[string]string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	// 1) settings.json：跳过危险模式确认
	cfgDir := strings.TrimSpace(env["CLAUDE_CONFIG_DIR"])
	if cfgDir == "" {
		cfgDir = filepath.Join(home, ".claude")
	}
	settingsPath := filepath.Join(cfgDir, "settings.json")
	settings := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		_ = json.Unmarshal(data, &settings)
	}
	if settings["skipDangerousModePermissionPrompt"] != true {
		settings["skipDangerousModePermissionPrompt"] = true
		if data, err := json.MarshalIndent(settings, "", "  "); err == nil {
			_ = os.MkdirAll(cfgDir, 0o700)
			_ = os.WriteFile(settingsPath, data, 0o600)
		}
	}
	// 2) .claude.json：信任当前工作区
	statePath := filepath.Join(home, ".claude.json")
	state := map[string]any{}
	if data, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(data, &state)
	}
	projects, _ := state["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	entry, _ := projects[workspace].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["hasTrustDialogAccepted"] = true
	entry["hasCompletedProjectOnboarding"] = true
	projects[workspace] = entry
	state["projects"] = projects
	if data, err := json.MarshalIndent(state, "", "  "); err == nil {
		_ = os.WriteFile(statePath, data, 0o600)
	}
}

// writeInteractiveRepoReport 扫描工作区里所有 git 仓库，记录仓库、分支、
// 远端和改动统计，作为交互提交的仓库计划/变更说明。
func writeInteractiveRepoReport(workspace string) {
	var b strings.Builder
	b.WriteString("# 交互会话仓库变更报告\n\n")
	entries, err := os.ReadDir(workspace)
	if err != nil {
		return
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".agentflow") {
			continue
		}
		repoDir := filepath.Join(workspace, e.Name())
		if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
			continue
		}
		count++
		branch := strings.TrimSpace(runGit(repoDir, "rev-parse", "--abbrev-ref", "HEAD"))
		remote := strings.TrimSpace(runGit(repoDir, "remote", "get-url", "origin"))
		status := strings.TrimSpace(runGit(repoDir, "status", "--porcelain"))
		stat := strings.TrimSpace(runGit(repoDir, "diff", "--stat"))
		b.WriteString("## " + e.Name() + "\n\n")
		b.WriteString("- 分支：" + branch + "\n- 远端：" + remote + "\n")
		if status == "" {
			b.WriteString("- 工作区：无未提交改动\n")
		} else {
			b.WriteString("- 工作区有改动：\n\n```\n" + clipText(status, 2000) + "\n```\n")
		}
		if stat != "" {
			b.WriteString("\n```\n" + clipText(stat, 2000) + "\n```\n")
		}
		b.WriteString("\n")
	}
	if count == 0 {
		b.WriteString("（本次会话未克隆任何仓库）\n")
	}
	_ = os.WriteFile(filepath.Join(workspace, "repository-changes.md"), []byte(b.String()), 0o644)
}

func runGit(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func clipText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n…（已截断）"
}

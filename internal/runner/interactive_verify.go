package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// workspaceHasCodeChanges 检查工作区内是否有未提交改动或未推送的本地提交。
// 交互式编码节点必须在有真实代码产出时才允许 pass。
func workspaceHasCodeChanges(workspace string) bool {
	checked := 0
	_ = filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(workspace, path)
		if strings.Count(rel, string(filepath.Separator)) > 2 {
			return filepath.SkipDir
		}
		if info.Name() != ".git" {
			return nil
		}
		repo := filepath.Dir(path)
		checked++
		if repoDirtyOrUnpushed(repo) {
			checked = -1
			return filepath.SkipAll
		}
		return filepath.SkipDir
	})
	return checked < 0
}

func repoDirtyOrUnpushed(repo string) bool {
	status := exec.Command("git", "-C", repo, "status", "--porcelain")
	if out, err := status.Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		return true
	}
	logs := exec.Command("git", "-C", repo, "log", "--branches", "--not", "--remotes", "--oneline", "-n", "1")
	if out, err := logs.Output(); err == nil && strings.TrimSpace(string(out)) != "" {
		return true
	}
	return false
}

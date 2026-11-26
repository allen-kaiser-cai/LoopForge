package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"agentflow/internal/domain"
)

// applyDesignRepoBinding 根据设计产物中“涉及仓库”一节收敛本次运行与工作项的仓库绑定。
// 仓库绑定不再固定成对：只有设计方案实际涉及的仓库参与后续编码/推分支/发布段。
func (e *Engine) applyDesignRepoBinding(runID string, node domain.WorkflowNode, workspace string) error {
	if !strings.EqualFold(strings.TrimSpace(node.ID), "design") {
		return nil
	}
	run, ok := e.repo.RunState(runID)
	if !ok {
		return nil
	}
	issueID := strings.TrimSpace(run.ExecutionContext.IssueID)
	if issueID == "" {
		return nil
	}
	issue, ok := e.repo.Issue(issueID)
	if !ok {
		return nil
	}
	rel := ""
	for _, c := range node.ArtifactOutputs {
		if strings.EqualFold(strings.TrimSpace(c.Name), "solution") {
			rel = strings.TrimSpace(c.Path)
			break
		}
	}
	if rel == "" {
		rel = "solution.md"
	}
	raw, err := os.ReadFile(filepath.Join(workspace, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	names := designRepoNames(string(raw))
	if len(names) == 0 {
		return errDesignReposMissing
	}
	projectRepos := []domain.Repository{}
	for _, r := range e.repo.Repositories() {
		if r.ProjectID == "" || r.ProjectID == run.ExecutionContext.ProjectID {
			projectRepos = append(projectRepos, r)
		}
	}
	matched := map[string]string{}
	ids := make([]string, 0, len(names))
	for _, name := range names {
		for _, r := range projectRepos {
			if strings.EqualFold(strings.TrimSpace(r.Name), name) {
				if _, seen := matched[r.ID]; !seen {
					matched[r.ID] = r.Name
					ids = append(ids, r.ID)
				}
				break
			}
		}
	}
	if len(ids) == 0 {
		return errDesignReposUnknown
	}
	if _, err := e.repo.PatchIssue(issueID, func(cur *domain.Issue) error {
		cur.RepositoryIDs = ids
		return nil
	}); err != nil {
		return err
	}
	// 收敛运行快照：后续节点只看到设计确定的仓库。
	kept := make([]domain.RepositoryContext, 0, len(ids))
	for _, rc := range run.RepositoryContexts() {
		if _, ok := matched[rc.RepositoryID]; ok {
			kept = append(kept, rc)
		}
	}
	if len(kept) > 0 {
		run.Repositories = kept
		if run.Repository.RepositoryID != "" {
			run.Repository = domain.RepositoryContext{}
		}
		if err := e.repo.PutRun(run); err != nil {
			return err
		}
	}
	_ = issue
	return nil
}

var designRepoBullet = regexp.MustCompile(`^\s*[-*+]\s*` + "`?" + `([A-Za-z0-9][A-Za-z0-9._-]*` + "`?" + `)`)

var errDesignReposMissing = &designRepoError{"设计方案缺少“## 涉及仓库”清单，请补充实际需要改动的仓库名后重新提交"}
var errDesignReposUnknown = &designRepoError{"设计方案列出的仓库都不属于当前项目，请检查仓库名是否正确"}

type designRepoError struct{ msg string }

func (e *designRepoError) Error() string { return e.msg }

func designRepoNames(text string) []string {
	lines := strings.Split(text, "\n")
	inside := false
	names := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			lower := strings.ToLower(trimmed)
			inside = strings.Contains(lower, "涉及仓库") || strings.Contains(lower, "changed repositories") || strings.Contains(lower, "repositories")
			continue
		}
		if !inside {
			continue
		}
		if m := designRepoBullet.FindStringSubmatch(line); len(m) == 2 {
			name := strings.TrimSpace(strings.Trim(m[1], "`"))
			if name != "" && !strings.Contains(name, " ") {
				names = append(names, name)
			}
		}
	}
	return names
}

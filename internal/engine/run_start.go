package engine

import (
	"agentflow/internal/domain"
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// assignWorkBranches gives every repository without a branch the first
// repository's work branch, else a generated one. A branch the caller
// explicitly continues or creates belongs to its own repository, and a
// read-only checkout has no work branch at all.
func assignWorkBranches(repositories []domain.RepositoryContext, generate func() string) {
	if len(repositories) == 0 {
		return
	}
	workBranch := ""
	if first := repositories[0]; !first.RequireExistingWorkBranch && !first.CreateNewWorkBranch && !first.ReadOnly {
		workBranch = strings.TrimSpace(first.WorkBranch)
	}
	for i := range repositories {
		if repositories[i].ReadOnly {
			repositories[i].WorkBranch = ""
			continue
		}
		if strings.TrimSpace(repositories[i].WorkBranch) == "" {
			if workBranch == "" {
				workBranch = generate()
			}
			repositories[i].WorkBranch = workBranch
		}
	}
}

func (e *Engine) Start(workflowID, input string) (domain.Run, error) {
	return e.StartWithContext(workflowID, input, domain.RepositoryContext{})
}

func (e *Engine) StartWithContext(workflowID, input string, repository domain.RepositoryContext) (domain.Run, error) {
	return e.StartWithRepositoriesContext(workflowID, input, []domain.RepositoryContext{repository}, domain.TelemetryContext{})
}

func (e *Engine) StartWithExecutionContext(workflowID, input string, repository domain.RepositoryContext, executionContext domain.TelemetryContext) (domain.Run, error) {
	return e.StartWithRepositoriesContext(workflowID, input, []domain.RepositoryContext{repository}, executionContext)
}

// StartWithRepositoriesContext starts a run with one or more repositories.
// All repositories are equals: each is checked out into a first-level
// sub-directory, gets the same work branch and, on scheduled runs, its own
// integration merge and MR. SOP deliverables stay at the run workspace root.
func (e *Engine) StartWithRepositoriesContext(workflowID, input string, repositories []domain.RepositoryContext, executionContext domain.TelemetryContext) (domain.Run, error) {
	return e.startWorkflow(workflowID, input, repositories, executionContext, false)
}

func (e *Engine) startWorkflow(workflowID, input string, repositories []domain.RepositoryContext, executionContext domain.TelemetryContext, workspace bool) (domain.Run, error) {
	wf, ok := e.repo.Workflow(workflowID)
	if !ok {
		return domain.Run{}, fmt.Errorf("workflow not found")
	}
	return e.startWorkflowDefinition(wf, input, repositories, executionContext, workspace)
}

// startWorkflowDefinition also accepts a run-only conversation workspace definition.
// It never writes a template to the catalog.
func (e *Engine) startWorkflowDefinition(wf domain.WorkflowDefinition, input string, repositories []domain.RepositoryContext, executionContext domain.TelemetryContext, workspace bool) (domain.Run, error) {
	workflowID := wf.ID
	repositories = normalizeRepositories(repositories)
	domain.AssignRepositoryDirs(repositories)
	e.mu.Lock()
	draining, executeRuns := e.draining, e.executeRuns
	instanceID, ttl := e.instanceID, e.leaseTTL
	e.mu.Unlock()
	if draining {
		return domain.Run{}, ErrExecutionDisabled
	}
	wf.CollaborationMode = ""
	wf.TaskTitle = ""
	wf.GroupMembers = nil
	wf.SOPCheckpoint = nil
	wf.GroupSOPHandoff = nil
	wf.GroupHistory = nil
	if wf.Generic {
		if strings.TrimSpace(executionContext.ProjectID) == "" {
			return domain.Run{}, fmt.Errorf("直接运行通用模板需要先选择项目")
		}
		if _, found := e.repo.Project(executionContext.ProjectID); !found {
			return domain.Run{}, fmt.Errorf("项目 %q 不存在，请刷新后重试", executionContext.ProjectID)
		}
	}
	wf = defaultWorkflowSandboxProfile(wf)
	wf, err := domain.AttachPreviewBuild(wf)
	if err != nil {
		return domain.Run{}, err
	}
	if err := validate(wf); err != nil {
		return domain.Run{}, err
	}
	profile, ok := e.repo.Sandbox(wf.SandboxProfileID)
	if !ok {
		return domain.Run{}, fmt.Errorf("sandbox profile %q not found", wf.SandboxProfileID)
	}
	if workspace && (profile.Driver != "docker" || profile.DockerfileID == "" || !(profile.ProjectID == executionContext.ProjectID || profile.Shared)) {
		return domain.Run{}, fmt.Errorf("对话工作区必须选择本项目或共享的 Docker 沙箱")
	}
	resources, err := resolveSandboxResources(e.repo, profile)
	if err != nil {
		return domain.Run{}, err
	}
	if _, err := workflowSandboxRequirements(e.repo, wf); err != nil {
		return domain.Run{}, err
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("run-%d", now.UnixNano())
	// 客户线：按工作项的 branchLine 把仓库基线切到对应环境线
	// （如禾城用 master-禾城农商行，资源中心用 dev_zyzx）。
	if issueID := strings.TrimSpace(executionContext.IssueID); issueID != "" {
		if issue, ok := e.repo.Issue(issueID); ok {
			line := strings.TrimSpace(issue.BranchLine)
			if line != "" {
				if project, found := e.repo.Project(issue.ProjectID); found {
					if def, hit := project.BranchLines[line]; hit && def.Disabled {
						note := strings.TrimSpace(def.Note)
						if note == "" {
							note = "该环境线不由本平台处理"
						}
						return domain.Run{}, fmt.Errorf("环境线 %s 已停用：%s", line, note)
					} else if def, hit := project.BranchLines[line]; hit && strings.TrimSpace(def.BaseBranch) != "" {
						for i := range repositories {
							repositories[i].BaseBranch = strings.TrimSpace(def.BaseBranch)
						}
						log.Printf("branch line %s: base branch -> %s (run %s)", line, def.BaseBranch, id)
					} else {
						log.Printf("branch line %s: 项目未配置该环境线，沿用默认基线 %s", line, firstRepositoryBase(repositories))
					}
				}
			}
		}
	}
	if wf.NoPreloadRepositories {
		log.Printf("workflow %s: noPreloadRepositories=true，运行不预加载仓库", wf.ID)
		repositories = nil
	}
	if wf.AnalysisOnly {
		// 分析模式：只拉基线最新代码，不创建功能分支（设计阶段只读分析用）。
		for i := range repositories {
			repositories[i].WorkBranch = ""
			repositories[i].CreateNewWorkBranch = false
		}
		log.Printf("workflow %s: analysisOnly=true，%d 个仓库按基线只读检出", wf.ID, len(repositories))
	}
	if !wf.AnalysisOnly {
		assignWorkBranches(repositories, func() string {
			issueID := strings.TrimSpace(executionContext.IssueID)
			if issueID != "" {
				if issue, ok := e.repo.Issue(issueID); ok {
					author := ""
					if user, found := e.repo.User(executionContext.ResponsibleUserID); found {
						author = user.LoginName
					}
					branch := domain.SpecWorkBranch(issue, author, now)
					valid := regexp.MustCompile(`^(feature|hotfix|refactoring|optimization)-[0-9]{8}-[a-z0-9]+-[^ /]+$`)
					if valid.MatchString(branch) {
						return branch
					}
					log.Printf("branch naming: %q 不符合 SSP 规范，回退默认命名", branch)
					return domain.DefaultWorkBranch(issue, now)
				}
			}
			return defaultWorkBranch(executionContext.IssueNumber, issueID, now)
		})
	}
	var firstRepository domain.RepositoryContext
	if len(repositories) > 0 {
		firstRepository = repositories[0]
	}
	executionContext.RunID, executionContext.TraceID, executionContext.WorkflowID, executionContext.WorkflowVersion = id, id, workflowID, wf.Version
	if executionContext.ParentRunID == "" {
		executionContext.WorkspaceRunID = ""
		// 执行目标按流程配置决定：
		// - personal：人机协同流程，固定落在责任人自己的电脑上；
		// - cloud：无人值守的标准动作，落在云池；
		// - 空值：沿用历史行为（云池优先，个人默认机兜底）。
		switch strings.ToLower(strings.TrimSpace(wf.ExecutionTarget)) {
		case "personal":
			executionContext.ExecutionPool = ""
			executionContext.ExecutionRunnerID = e.repo.DefaultRunner(executionContext.ResponsibleUserID)
			if executionContext.ExecutionRunnerID == "" {
				return domain.Run{}, fmt.Errorf("该流程要求在人机协同模式下执行，但责任人（用户 %d）还没有绑定默认执行机，请先在执行机器页面绑定", executionContext.ResponsibleUserID)
			}
		case "cloud":
			executionContext.ExecutionPool = domain.ExecutionModeCloud
			if runner, ok := e.cloudPoolRunner(executionContext.ProjectID); ok {
				executionContext.ExecutionRunnerID = runner.ID
			}
		default:
			if e.cloudPoolServesProject(executionContext.ProjectID) {
				executionContext.ExecutionPool = domain.ExecutionModeCloud
				if runner, ok := e.cloudPoolRunner(executionContext.ProjectID); ok {
					executionContext.ExecutionRunnerID = runner.ID
				}
			} else {
				executionContext.ExecutionRunnerID = e.repo.DefaultRunner(executionContext.ResponsibleUserID)
			}
		}
	}
	if executionContext.ExecutionRunnerID == "" {
		if runner, ok := e.cloudPoolRunner(executionContext.ProjectID); ok {
			executionContext.ExecutionRunnerID = runner.ID
			executionContext.ExecutionPool = domain.ExecutionModeCloud
		} else if e.cloudPoolRequired(executionContext.ProjectID) {
			// The first node binds a pool runner once one is online.
			executionContext.ExecutionPool = domain.ExecutionModeCloud
		}
	}
	if executionContext.ExecutionRunnerID != "" || executionContext.ExecutionPool == domain.ExecutionModeCloud {
		executionContext.ExecutionMode = domain.ExecutionModeRunner
		executionContext.RunnerWorkspaceLayout = domain.RunnerWorkspaceLayoutShared
	} else {
		executionContext.ExecutionMode = domain.ExecutionModeCloud
	}
	if err := e.requireWorkBranchCapabilities(executionContext.ExecutionRunnerID, repositories); err != nil {
		return domain.Run{}, err
	}
	wf.SandboxSnapshot = resolvedSandboxSnapshot(profile, resources)
	r := domain.Run{
		ID: id, WorkflowID: workflowID, WorkflowSnapshot: wf, Status: "running", Input: input,
		Repository: firstRepository, ExecutionContext: executionContext, Nodes: map[string]*domain.NodeRun{},
		CreatedAt: now, UpdatedAt: now,
	}
	if len(repositories) > 0 {
		r.Repositories = append([]domain.RepositoryContext(nil), repositories...)
	}
	for _, n := range wf.Nodes {
		r.Nodes[n.ID] = &domain.NodeRun{NodeID: n.ID, Status: "pending"}
	}
	if executionContext.ParentRunID != "" {
		parent, exists := e.repo.RunState(executionContext.ParentRunID)
		if !exists {
			return domain.Run{}, fmt.Errorf("父 SOP 运行不存在")
		}
		if err := inheritSOPHandoff(parent, &r); err != nil {
			return domain.Run{}, err
		}
	}
	if workspace {
		r.Kind = domain.RunKindWorkspace
		r.Status = "waiting"
	}
	if workspace {
		e.event(&r, "", "workspace.created", "对话工作区已创建")
	} else {
		e.event(&r, "", "run.started", "流水线开始执行")
	}
	if executeRuns && !workspace {
		if strings.TrimSpace(instanceID) == "" || ttl <= 0 {
			return domain.Run{}, ErrExecutionDisabled
		}
		r.OwnerInstanceID, r.Generation, r.LeaseUntil = instanceID, 1, now.Add(ttl)
	}
	// Control plane persists an unowned running row. The worker claims it
	// through ResumeInterruptedRuns. Combined instances still insert already
	// leased so another process cannot start a second execute() on the same run.
	if err := e.repo.PutRun(r); err != nil {
		return domain.Run{}, err
	}
	if current, ok := e.repo.RunState(r.ID); ok {
		r = current
	}
	e.publish(r.ID)
	if !executeRuns || r.Status == "queued" || workspace {
		return r, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	if !e.claimRunning(id, cancel) {
		cancel()
		return r, nil
	}
	go e.execute(ctx, id)
	return r, nil
}

func (e *Engine) executes() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.executeRuns && !e.draining
}

func firstRepositoryBase(repos []domain.RepositoryContext) string {
	for _, r := range repos {
		if strings.TrimSpace(r.BaseBranch) != "" {
			return r.BaseBranch
		}
	}
	return "master"
}

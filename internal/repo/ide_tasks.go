package repo

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// IDETask 是 IDE 模式下平台派给开发者的交互任务。
type IDETask struct {
	ID          string          `json:"id"`
	RunID       string          `json:"runId"`
	NodeID      string          `json:"nodeId"`
	ProjectID   string          `json:"projectId"`
	IssueID     string          `json:"issueId"`
	UserID      int64           `json:"userId"`
	Status      string          `json:"status"`
	Decision    string          `json:"decision"`
	Note        string          `json:"note"`
	Artifacts   json.RawMessage `json:"artifacts"`
	BranchName  string          `json:"branchName"`
	ConfirmTok  string          `json:"-"`
	RemindAt    *time.Time      `json:"remindAt,omitempty"`
	FailAt      *time.Time      `json:"failAt,omitempty"`
	SubmittedAt *time.Time      `json:"submittedAt,omitempty"`
	CreatedAt   *time.Time      `json:"createdAt,omitempty"`
	UpdatedAt   *time.Time      `json:"updatedAt,omitempty"`
}

const ideTaskColumns = `id, run_id, node_id, project_id, issue_id, user_id, status,
	COALESCE(decision,''), COALESCE(note,''), COALESCE(artifacts_json,'{}'),
	COALESCE(branch_name,''), COALESCE(confirm_token,''), remind_at, fail_at, submitted_at, created_at, updated_at`

func scanIDETask(row interface{ Scan(...any) error }) (IDETask, bool) {
	var t IDETask
	var artifacts string
	var remind, fail, submitted, created, updated sql.NullTime
	if err := row.Scan(&t.ID, &t.RunID, &t.NodeID, &t.ProjectID, &t.IssueID, &t.UserID,
		&t.Status, &t.Decision, &t.Note, &artifacts, &t.BranchName, &t.ConfirmTok,
		&remind, &fail, &submitted, &created, &updated); err != nil {
		return IDETask{}, false
	}
	t.Artifacts = json.RawMessage(artifacts)
	if remind.Valid {
		t.RemindAt = &remind.Time
	}
	if fail.Valid {
		t.FailAt = &fail.Time
	}
	if submitted.Valid {
		t.SubmittedAt = &submitted.Time
	}
	if created.Valid {
		t.CreatedAt = &created.Time
	}
	if updated.Valid {
		t.UpdatedAt = &updated.Time
	}
	return t, true
}

// CreateIDETask 幂等地创建一条 IDE 交互任务（同一运行+节点只保留一条）。
func (r *RunsRepo) CreateIDETask(t IDETask) error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("ide task id is required")
	}
	now := time.Now().UTC()
	if t.Status == "" {
		t.Status = "waiting"
	}
	err := r.DB.Exec(`INSERT INTO ide_interactive_tasks
		(id, run_id, node_id, project_id, issue_id, user_id, status, artifacts_json, branch_name, remind_at, fail_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON DUPLICATE KEY UPDATE status=VALUES(status), updated_at=VALUES(updated_at)`,
		t.ID, t.RunID, t.NodeID, t.ProjectID, t.IssueID, t.UserID, t.Status,
		string(t.Artifacts), t.BranchName, t.RemindAt, t.FailAt, now, now).Error
	return err
}

// IDETask 按 ID 读取。
func (r *RunsRepo) IDETask(id string) (IDETask, bool) {
	row := r.DB.Raw(`SELECT `+ideTaskColumns+` FROM ide_interactive_tasks WHERE id = ?`, id).Row()
	return scanIDETask(row)
}

// IDETasksForUser 返回某账号的待处理任务。
func (r *RunsRepo) IDETasksForUser(userID int64) []IDETask {
	rows, err := r.DB.Raw(`SELECT `+ideTaskColumns+` FROM ide_interactive_tasks
		WHERE user_id = ? AND status IN ('waiting','requested') ORDER BY created_at`, userID).Rows()
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []IDETask{}
	for rows.Next() {
		if t, ok := scanIDETask(rows); ok {
			out = append(out, t)
		}
	}
	return out
}

// IDETaskArtifacts 解析产物列表。
func (t IDETask) ArtifactList() map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal(t.Artifacts, &out)
	return out
}

// PutIDETaskArtifact 追加/覆盖一个产物（name -> content）。
func (r *RunsRepo) PutIDETaskArtifact(id, name, content string) error {
	t, ok := r.IDETask(id)
	if !ok {
		return fmt.Errorf("ide task not found")
	}
	arts := t.ArtifactList()
	arts[name] = content
	buf, _ := json.Marshal(arts)
	return r.DB.Exec(`UPDATE ide_interactive_tasks SET artifacts_json=?, updated_at=? WHERE id=?`,
		string(buf), time.Now().UTC(), id).Error
}

// RequestIDETaskSubmit 记录提交申请并生成确认 token。
func (r *RunsRepo) RequestIDETaskSubmit(id, decision, note, token string) error {
	now := time.Now().UTC()
	return r.DB.Exec(`UPDATE ide_interactive_tasks
		SET status='requested', decision=?, note=?, confirm_token=?, updated_at=?
		WHERE id=? AND status IN ('waiting','requested')`, decision, note, token, now, id).Error
}

// ConfirmIDETask 凭 token 确认提交。
func (r *RunsRepo) ConfirmIDETask(id, token string) error {
	now := time.Now().UTC()
	return r.DB.Exec(`UPDATE ide_interactive_tasks
		SET status='confirmed', confirm_token='', submitted_at=?, updated_at=?
		WHERE id=? AND confirm_token=? AND status='requested'`, now, now, id, token).Error
}

// MarkIDETaskStatus 更新状态（超时/失败等）。
func (r *RunsRepo) MarkIDETaskStatus(id, status string) error {
	return r.DB.Exec(`UPDATE ide_interactive_tasks SET status=?, updated_at=? WHERE id=?`,
		status, time.Now().UTC(), id).Error
}

// InteractiveMode 返回账号的交互方式：ide（默认）或 terminal。
func (r *UsersRepo) InteractiveMode(userID int64) string {
	if userID <= 0 {
		return "ide"
	}
	var mode string
	if err := r.DB.Raw(`SELECT COALESCE(interactive_mode,'ide') FROM user_configs WHERE user_id = ?`, userID).Scan(&mode).Error; err != nil || strings.TrimSpace(mode) == "" {
		return "ide"
	}
	return strings.ToLower(strings.TrimSpace(mode))
}

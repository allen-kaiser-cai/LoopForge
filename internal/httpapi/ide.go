package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ideHandlers struct{ *deps }

func (h *ideHandlers) current(c *gin.Context) (int64, bool) {
	u, ok := h.deps.requestUser(c.Request, true)
	if !ok || u == nil || u.ID <= 0 {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "请先登录或提供访问令牌"})
		return 0, false
	}
	return u.ID, true
}

// GET /api/ide/tasks
func (h *ideHandlers) tasks(c *gin.Context) {
	uid, ok := h.current(c)
	if !ok {
		return
	}
	items := h.repo.IDETasksForUser(uid)
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// GET /api/ide/tasks/:id
func (h *ideHandlers) task(c *gin.Context) {
	uid, ok := h.current(c)
	if !ok {
		return
	}
	t, found := h.repo.IDETask(c.Param("id"))
	if !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "任务不存在"})
		return
	}
	if t.UserID != uid {
		c.JSON(http.StatusForbidden, map[string]string{"error": "只能查看本人的交互任务"})
		return
	}
	issue, _ := h.repo.Issue(t.IssueID)
	repos := []map[string]string{}
	for _, rid := range issue.RepositoryIDs {
		if rc, ok := h.repo.Repository(rid); ok {
			repos = append(repos, map[string]string{
				"name": rc.Name, "baseBranch": rc.DefaultBranch, "cloneUrl": rc.CloneURL, "workspaceDir": rc.Name,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"id": t.ID, "runId": t.RunID, "nodeId": t.NodeID, "status": t.Status,
		"branchName": t.BranchName, "decision": t.Decision, "note": t.Note,
		"issueTitle": issue.Title, "issueDescription": issue.Description,
		"repositories": repos, "artifacts": t.ArtifactList(),
	})
}

// POST /api/ide/tasks/:id/artifacts  {name, content}
func (h *ideHandlers) artifact(c *gin.Context) {
	uid, ok := h.current(c)
	if !ok {
		return
	}
	t, found := h.repo.IDETask(c.Param("id"))
	if !found || t.UserID != uid {
		c.JSON(http.StatusForbidden, map[string]string{"error": "任务不存在或无权操作"})
		return
	}
	var body struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if !decode(c, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "name 不能为空"})
		return
	}
	if err := h.repo.PutIDETaskArtifact(t.ID, body.Name, body.Content); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "artifacts": len(t.ArtifactList()) + 1})
}

// POST /api/ide/tasks/:id/request-submit  {decision, note}
func (h *ideHandlers) requestSubmit(c *gin.Context) {
	uid, ok := h.current(c)
	if !ok {
		return
	}
	t, found := h.repo.IDETask(c.Param("id"))
	if !found || t.UserID != uid {
		c.JSON(http.StatusForbidden, map[string]string{"error": "任务不存在或无权操作"})
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if !decode(c, &body) {
		return
	}
	decision := strings.ToLower(strings.TrimSpace(body.Decision))
	if decision != "pass" && decision != "rework" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "decision 只能是 pass 或 rework"})
		return
	}
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	token := hex.EncodeToString(buf)
	if err := h.repo.RequestIDETaskSubmit(t.ID, decision, body.Note, token); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"ok": true, "status": "requested",
		"confirmUrl": fmt.Sprintf("/ide/confirm/%s?token=%s", t.ID, token),
		"message":    "已申请提交，请让责任人在钉钉/浏览器中确认后生效",
	})
}

// GET/POST /api/ide/tasks/:id/confirm?token=...
func (h *ideHandlers) confirm(c *gin.Context) {
	token := strings.TrimSpace(c.Query("token"))
	if c.Request.Method == http.MethodPost {
		var body struct {
			Token string `json:"token"`
		}
		if decode(c, &body) {
			token = strings.TrimSpace(body.Token)
		}
	}
	if token == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "缺少确认 token"})
		return
	}
	if err := h.repo.ConfirmIDETask(c.Param("id"), token); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "确认失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "status": "confirmed"})
}

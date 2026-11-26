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
		"confirmUrl": fmt.Sprintf("/api/ide/tasks/%s/confirm?token=%s", t.ID, token),
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
	err := h.repo.ConfirmIDETask(c.Param("id"), token)
	if c.Request.Method == http.MethodGet && !strings.Contains(c.GetHeader("Accept"), "application/json") {
		h.renderConfirmPage(c, err)
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "确认失败：" + err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "status": "confirmed"})
}

// renderConfirmPage 给钉钉里点开的确认链接返回一个简单的结果页。
func (h *ideHandlers) renderConfirmPage(c *gin.Context, err error) {
	ok := err == nil
	title := "确认成功"
	message := "已确认提交，流程会自动继续，可以关闭本页面。"
	color := "#059669"
	icon := "✓"
	if !ok {
		title = "确认失败"
		message = err.Error()
		color = "#DC2626"
		icon = "!"
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>`+title+` · LoopForge</title>
<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#f2f6fc;font-family:-apple-system,"Microsoft YaHei",sans-serif;color:#0f172a}
.card{background:#fff;border:1px solid #dbe6f3;border-radius:16px;padding:40px 36px;max-width:480px;width:88%;box-shadow:0 12px 32px rgba(15,23,42,.08);text-align:center}
.icon{width:64px;height:64px;border-radius:50%;background:`+color+`14;color:`+color+`;font-size:34px;font-weight:700;display:flex;align-items:center;justify-content:center;margin:0 auto 18px}
h1{font-size:22px;margin:0 0 10px}p{color:#475569;line-height:1.7;margin:0;font-size:14px}
.brand{margin-top:24px;font-size:12px;color:#94a3b8}
</style></head><body><div class="card"><div class="icon">`+icon+`</div><h1>`+title+`</h1><p>`+message+`</p>
<div class="brand">LoopForge · IDE + MCP 交互任务</div></div></body></html>`)
}

// GET/PUT /api/users/:id/interactive-mode
func (h *ideHandlers) interactiveMode(c *gin.Context) {
	u, ok := h.deps.requestUser(c.Request, true)
	if !ok || u == nil {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "请先登录"})
		return
	}
	if c.Param("id") != fmt.Sprintf("%d", u.ID) {
		c.JSON(http.StatusForbidden, map[string]string{"error": "只能修改本人的交互方式"})
		return
	}
	if c.Request.Method == http.MethodGet {
		c.JSON(http.StatusOK, map[string]string{"mode": h.repo.InteractiveMode(u.ID)})
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if !decode(c, &body) {
		return
	}
	if err := h.repo.SetInteractiveMode(u.ID, body.Mode); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"mode": strings.ToLower(strings.TrimSpace(body.Mode))})
}

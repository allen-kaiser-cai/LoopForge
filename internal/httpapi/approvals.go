package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"agentflow/internal/domain"
)

// pendingApprovals 只返回当前登录账号有权处理的待审批节点。
// 平台管理员会看到全部待审批项，并带 adminBypass 标记，便于应急时定位。
func (h *runsHandlers) pendingApprovals(c *gin.Context) {
	user, ok := h.requestUser(c.Request, true)
	if !ok || user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	filter := domain.AnalyticsFilter{ProjectID: c.Query("projectId")}
	summaries, err := h.repo.RunSummariesFor(filter, "")
	if err != nil {
		respond(c, err)
		return
	}
	out := make([]gin.H, 0, 8)
	for _, summary := range summaries {
		if summary.Status != "waiting" {
			continue
		}
		view, exists := h.repo.RunView(summary.ID)
		if !exists {
			continue
		}
		nodeID := ""
		nodeStatus := time.Time{}
		for id, node := range view.Nodes {
			if node != nil && node.Status == "waiting_approval" {
				nodeID = id
				nodeStatus = node.StartedAt
				break
			}
		}
		if nodeID == "" {
			continue
		}
		var node domain.WorkflowNode
		for _, n := range view.WorkflowSnapshot.Nodes {
			if n.ID == nodeID {
				node = n
				break
			}
		}
		allowed := len(node.Approvers) == 0 || user.IsPlatformAdmin
		if !allowed {
			for _, want := range node.Approvers {
				if strings.EqualFold(strings.TrimSpace(want), user.LoginName) {
					allowed = true
					break
				}
			}
		}
		if !allowed {
			continue
		}
		out = append(out, gin.H{
			"runId":         view.ID,
			"projectId":     view.ExecutionContext.ProjectID,
			"workflowId":    view.WorkflowID,
			"nodeId":        nodeID,
			"nodeName":      node.Name,
			"approverLabel": node.ApproverLabel,
			"approvers":     node.Approvers,
			"adminBypass":   user.IsPlatformAdmin && !containsLogin(node.Approvers, user.LoginName),
			"waitingSince":  nodeStatus,
			"createdAt":     view.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, out)
}

func containsLogin(list []string, login string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), login) {
			return true
		}
	}
	return false
}

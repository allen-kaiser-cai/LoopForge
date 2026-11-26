package engine

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// stalledRunRecoveryLoop 定期发现“执行实例退出，等待恢复”的运行并自动重试对应节点，
// 避免平台发布/重启后交互会话一直停在等待状态。
func (e *Engine) stalledRunRecoveryLoop(ctx context.Context) {
	if !e.executes() {
		return
	}
	attempted := struct {
		sync.Mutex
		m map[string]bool
	}{m: map[string]bool{}}
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, listed := range e.repo.Runs() {
			if listed.Status != "running" {
				continue
			}
			for nodeID, nr := range listed.Nodes {
				if nr == nil || (nr.Status != "running" && nr.Status != "pending") {
					continue
				}
				if !strings.Contains(nr.Activity, "等待恢复") {
					continue
				}
				key := listed.ID + "/" + nodeID
				attempted.Lock()
				done := attempted.m[key]
				attempted.m[key] = true
				attempted.Unlock()
				if done {
					continue
				}
				if err := e.Retry(listed.ID, nodeID, "平台重启后自动恢复", ""); err != nil {
					log.Printf("auto recover run %s node %s: %v", listed.ID, nodeID, err)
					continue
				}
				log.Printf("auto recovered run %s node %s after restart", listed.ID, nodeID)
			}
		}
	}
}

package engine

import (
	"context"
	"log"
	"time"
)

// queuedRunPromotionLoop 周期性提升排队中的运行。
// 运行准入按项目/执行机限额排队；此前只在进程启动时提升一次，
// 若队列中的运行是本次进程内创建的，就可能永远停在 queued。
func (e *Engine) queuedRunPromotionLoop(ctx context.Context) {
	if !e.executes() {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		ids, err := e.repo.PromoteQueuedRuns()
		if err != nil {
			log.Printf("promote queued runs: %v", err)
			continue
		}
		for _, id := range ids {
			e.publish(id)
			go e.execute(ctx, id)
		}
	}
}

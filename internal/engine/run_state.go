package engine

import (
	"agentflow/internal/domain"
	"context"
	"fmt"
	"log"
	"time"
)

func (e *Engine) updateRun(id string, mutate func(*domain.Run)) {
	_ = e.updateRunAt(id, false, mutate)
}

// updateRunErr is updateRun for callers that must react to a failed write,
// such as a SOP node whose artifact handoff did not reach the store.
func (e *Engine) updateRunErr(id string, mutate func(*domain.Run)) error {
	_, err := e.updateRunAtErr(id, false, mutate)
	return err
}

func (e *Engine) updateRunSettling(id string, mutate func(*domain.Run)) bool {
	return e.updateRunAt(id, true, mutate)
}

func (e *Engine) updateRunAt(id string, allowExpiredLease bool, mutate func(*domain.Run)) bool {
	updated, _ := e.updateRunAtErr(id, allowExpiredLease, mutate)
	return updated
}

// updateRunAtErr reports whether the run was stored and, when the store
// rejected the write, that error. A skipped update (missing run or a lease
// held elsewhere) is not an error.
func (e *Engine) updateRunAtErr(id string, allowExpiredLease bool, mutate func(*domain.Run)) (bool, error) {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()
	r, ok := e.repo.RunState(id)
	if !ok {
		_ = e.takePendingObservations(id)
		return false, nil
	}
	if isLiveStatus(r.Status) && r.OwnerInstanceID != "" && !e.ownsRun(r) && !allowExpiredLease {
		_ = e.takePendingObservations(id)
		return false, nil
	}
	pending := e.takePendingObservations(id)
	if mutate == nil && len(pending) == 0 {
		return true, nil
	}
	for _, item := range pending {
		e.applyObservation(&r, item.nodeID, item.event)
	}
	if mutate != nil {
		mutate(&r)
	}
	if err := e.repo.PutRun(r); err != nil {
		// 常见失败是单个产物超过列上限（Data too long）。先裁掉超限内容再重试一次，
		// 避免一次写失败把整条运行永久卡在 running。
		if shrinkOversizedArtifacts(&r) {
			if retry := e.repo.PutRun(r); retry == nil {
				log.Printf("put run %s: recovered after shrinking oversized artifacts (first error: %v)", id, err)
				if current, ok := e.repo.RunStreamView(id); ok {
					e.publishRun(current)
				}
				return true, nil
			} else {
				log.Printf("put run %s retry after shrink: %v", id, retry)
			}
		}
		log.Printf("put run %s: %v", id, err)
		return false, err
	}
	if current, ok := e.repo.RunStreamView(id); ok {
		e.publishRun(current)
	}
	return true, nil
}

// Subscribe provides provider-neutral live run snapshots. Slow clients receive
// the newest state rather than blocking execution workers.
func (e *Engine) Subscribe(runID string) (<-chan domain.Run, func(), error) {
	current, ok := e.repo.RunStreamView(runID)
	if !ok {
		return nil, nil, fmt.Errorf("run not found")
	}
	ch := make(chan domain.Run, 1)
	e.subMu.Lock()
	if e.subs[runID] == nil {
		e.subs[runID] = map[chan domain.Run]struct{}{}
	}
	e.subs[runID][ch] = struct{}{}
	e.subMu.Unlock()
	ch <- current
	stop := make(chan struct{})
	go e.pollRun(runID, stop, current.Revision)
	cancel := func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		e.subMu.Lock()
		if _, exists := e.subs[runID][ch]; exists {
			delete(e.subs[runID], ch)
			close(ch)
		}
		if len(e.subs[runID]) == 0 {
			delete(e.subs, runID)
		}
		e.subMu.Unlock()
	}
	return ch, cancel, nil
}

func (e *Engine) pollRun(runID string, stop <-chan struct{}, revision int) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			latestRevision, ok := e.repo.RunRevision(runID)
			if !ok {
				return
			}
			if latestRevision == revision {
				continue
			}
			current, ok := e.repo.RunStreamView(runID)
			if !ok {
				return
			}
			if current.Revision == revision {
				continue
			}
			revision = current.Revision
			e.publishRun(current)
		}
	}
}

func (e *Engine) StartClaimLoop(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		e.ResumeInterruptedRuns()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !e.executes() {
					return
				}
				if resumed := e.ResumeInterruptedRuns(); len(resumed) > 0 {
					log.Printf("claimed %d run(s): %v", len(resumed), resumed)
				}
			}
		}
	}()
}

func (e *Engine) publish(runID string) {
	current, ok := e.repo.RunState(runID)
	if !ok {
		return
	}
	e.publishRun(current)
}

func (e *Engine) publishRun(current domain.Run) {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	for ch := range e.subs[current.ID] {
		select {
		case ch <- current:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- current:
			default:
			}
		}
	}
}

// shrinkOversizedArtifacts 把超过数据库列安全上限的产物内容截断，返回是否有改动。
// 仅用于持久化失败后的兜底重试，正常路径由产物契约校验与 appendArtifact 控制大小。
func shrinkOversizedArtifacts(r *domain.Run) bool {
	const safeLimit = 60000
	changed := false
	for i := range r.Artifacts {
		if len(r.Artifacts[i].Content) <= safeLimit {
			continue
		}
		note := "\n\n> 产物超过单次保存上限，已截断；完整内容请查看运行工作区。\n"
		keep := safeLimit - len(note)
		if keep < 0 {
			keep = 0
		}
		r.Artifacts[i].Content = r.Artifacts[i].Content[:keep] + note
		changed = true
	}
	return changed
}

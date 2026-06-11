package lifecycle

import (
	"fmt"
	"time"

	"github.com/gpu-sched-cli/internal/dag"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/queue"
	"github.com/gpu-sched-cli/internal/scheduler"
	"github.com/gpu-sched-cli/internal/store"
)

type Manager struct {
	store     *store.Store
	pq        *queue.PriorityQueue
	scheduler *scheduler.Scheduler
	stopCh    chan struct{}
}

func NewManager(s *store.Store, pq *queue.PriorityQueue, sched *scheduler.Scheduler) *Manager {
	return &Manager{
		store:     s,
		pq:        pq,
		scheduler: sched,
		stopCh:    make(chan struct{}),
	}
}

func (m *Manager) Start() {
	go m.runTimeoutChecker()
	go m.runRetryChecker()
	go m.runGangTimeoutChecker()
	go m.runUsageAccounting()
	go m.runDepTimeoutChecker()
}

func (m *Manager) Stop() {
	close(m.stopCh)
}

func (m *Manager) runTimeoutChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.checkTimeouts()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) checkTimeouts() {
	cfg := m.store.GetConfig()
	if !cfg.TimeoutEnabled {
		return
	}
	running := m.store.GetRunningTasks()
	for _, t := range running {
		if t.StartedAt == nil {
			continue
		}
		maxDuration := time.Duration(float64(t.Spec.EstimatedMin)*cfg.TimeoutMultiplier) * time.Minute
		if time.Since(*t.StartedAt) > maxDuration {
			m.store.ReleaseTaskGPUs(t.ID)
			m.store.UpdateTaskStatus(t.ID, model.TaskStatusTimedOut)
		}
	}
}

func (m *Manager) runRetryChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.checkRetries()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) checkRetries() {
	cfg := m.store.GetConfig()
	failedTasks := m.store.GetTasksByStatus(model.TaskStatusFailed)
	for _, t := range failedTasks {
		if t.RetryCount >= cfg.MaxRetries {
			continue
		}
		if t.NextRetryAt != nil && time.Now().Before(*t.NextRetryAt) {
			continue
		}
		interval := time.Duration(1<<uint(t.RetryCount)) * time.Minute
		nextRetry := time.Now().Add(interval)
		m.store.SetTaskRetry(t.ID, t.RetryCount+1, nextRetry)
		m.pq.RequeueFront(t)
	}
}

func (m *Manager) runGangTimeoutChecker() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.scheduler.HandleGangTimeout()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) runUsageAccounting() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.accountUsage()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) accountUsage() {
	running := m.store.GetRunningTasks()
	for _, t := range running {
		gpuCount := t.GPUCount
		if gpuCount == 0 {
			gpuCount = len(t.AllocatedGPUs)
		}
		if gpuCount > 0 {
			m.store.AddUserUsage(t.Spec.User, float64(gpuCount))
		}
	}
}

func (m *Manager) runDepTimeoutChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.checkDepTimeouts()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) checkDepTimeouts() {
	audit := m.store.GetAuditLogger()
	newlyUnblocked := m.store.CheckDepTimeouts()
	for _, taskID := range newlyUnblocked {
		task := m.store.GetTask(taskID)
		if task != nil {
			m.pq.Enqueue(task)
			audit.Record(model.AuditDecisionUnblocked, taskID, nil,
				fmt.Sprintf("%s unblocked: dependencies satisfied (some may have timed out)", taskID), nil)
		}
	}
}

func (m *Manager) CompleteTask(id string) {
	t := m.store.GetTask(id)
	if t == nil {
		return
	}
	m.store.ReleaseTaskGPUs(id)
	m.store.UpdateTaskStatus(id, model.TaskStatusCompleted)
	m.checkDependentsUnblock(id)
}

func (m *Manager) FailTask(id string) {
	t := m.store.GetTask(id)
	if t == nil {
		return
	}
	m.store.ReleaseTaskGPUs(id)
	m.store.UpdateTaskStatus(id, model.TaskStatusFailed)

	audit := m.store.GetAuditLogger()
	graph := m.store.GetDepGraph()
	dag.CascadeSkip(graph, id,
		func(taskID string) *model.Task { return m.store.GetTask(taskID) },
		func(taskID string, status model.TaskStatus) { m.store.UpdateTaskStatus(taskID, status) },
		audit.Record,
	)
	m.checkDependentsUnblock(id)
}

func (m *Manager) CancelTask(id string) {
	t := m.store.GetTask(id)
	if t == nil {
		return
	}
	if t.Status == model.TaskStatusRunning {
		m.store.ReleaseTaskGPUs(id)
	}
	m.store.UpdateTaskStatus(id, model.TaskStatusCancelled)

	audit := m.store.GetAuditLogger()
	graph := m.store.GetDepGraph()
	dag.CascadeSkip(graph, id,
		func(taskID string) *model.Task { return m.store.GetTask(taskID) },
		func(taskID string, status model.TaskStatus) { m.store.UpdateTaskStatus(taskID, status) },
		audit.Record,
	)
	m.checkDependentsUnblock(id)
}

func (m *Manager) checkDependentsUnblock(taskID string) {
	audit := m.store.GetAuditLogger()
	graph := m.store.GetDepGraph()
	dependents := graph.Dependents(taskID)
	for _, depID := range dependents {
		depTask := m.store.GetTask(depID)
		if depTask != nil && depTask.Status == model.TaskStatusBlocked {
			if m.store.HasFailedDependency(depID) {
				continue
			}
			if m.store.AreDependenciesSatisfied(depID) {
				m.store.UpdateTaskStatus(depID, model.TaskStatusQueued)
				m.pq.Enqueue(depTask)
				audit.Record(model.AuditDecisionUnblocked, depID, nil,
					fmt.Sprintf("%s unblocked: dependencies satisfied", depID), nil)
			}
		}
	}
}

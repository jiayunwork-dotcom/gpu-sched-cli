package store

import (
	"fmt"
	"sync"
	"time"

	"github.com/gpu-sched-cli/internal/dag"
	"github.com/gpu-sched-cli/internal/model"
)

type Store struct {
	mu          sync.RWMutex
	cluster     *model.Cluster
	tasks       map[string]*model.Task
	schedConfig *model.SchedulerConfig
	userUsage   map[string]float64
	taskCounter int
	audit       *AuditLogger
	depGraph    *dag.DependencyGraph
}

func NewStore(cluster *model.Cluster, config *model.SchedulerConfig) *Store {
	return &Store{
		cluster:     cluster,
		tasks:       make(map[string]*model.Task),
		schedConfig: config,
		userUsage:   make(map[string]float64),
		audit:       NewAuditLogger(),
		depGraph:    dag.NewDependencyGraph(),
	}
}

func (s *Store) GetAuditLogger() *AuditLogger {
	return s.audit
}

func (s *Store) GetCluster() *model.Cluster {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cluster
}

func (s *Store) GetConfig() *model.SchedulerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.schedConfig
}

func (s *Store) SetConfig(cfg *model.SchedulerConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schedConfig = cfg
}

func (s *Store) AddTask(spec *model.TaskSpec) *model.Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskCounter++
	task := &model.Task{
		ID:           fmt.Sprintf("task-%04d", s.taskCounter),
		Spec:         *spec,
		Status:       model.TaskStatusSubmitted,
		SubmittedAt:  time.Now(),
		QueueEnterAt: time.Now(),
		AllocatedGPUs: []string{},
	}
	s.tasks[task.ID] = task
	return task
}

func (s *Store) GetTask(id string) *model.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tasks[id]
}

func (s *Store) UpdateTaskStatus(id string, status model.TaskStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Status = status
		now := time.Now()
		switch status {
		case model.TaskStatusRunning:
			t.StartedAt = &now
		case model.TaskStatusCompleted, model.TaskStatusFailed, model.TaskStatusTimedOut:
			t.FinishedAt = &now
		}
	}
}

func (s *Store) SetTaskAllocatedGPUs(id string, gpuIDs []string, crossNode bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.AllocatedGPUs = gpuIDs
		t.GPUCount = len(gpuIDs)
		t.CrossNode = crossNode
	}
}

func (s *Store) ReleaseTaskGPUs(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return
	}
	for _, gpuID := range t.AllocatedGPUs {
		gpu := s.cluster.FindGPUByID(gpuID)
		if gpu == nil {
			continue
		}
		perGPUMem := t.Spec.GPUReq.MinMemory / len(t.AllocatedGPUs)
		gpu.AllocatedMemory -= perGPUMem
		if gpu.AllocatedMemory < 0 {
			gpu.AllocatedMemory = 0
		}
		newTaskIDs := make([]string, 0, len(gpu.TaskIDs))
		for _, tid := range gpu.TaskIDs {
			if tid != id {
				newTaskIDs = append(newTaskIDs, tid)
			}
		}
		gpu.TaskIDs = newTaskIDs
		if len(gpu.TaskIDs) == 0 {
			gpu.Status = model.GPUStatusFree
			gpu.AllocatedMemory = 0
		} else {
			gpu.Status = model.GPUStatusShared
		}
		node := s.cluster.Nodes[gpu.NodeName]
		if node != nil {
			node.UsedCPU -= t.Spec.CPUReq
			node.UsedMemory -= t.Spec.MemoryReq
			if node.UsedCPU < 0 {
				node.UsedCPU = 0
			}
			if node.UsedMemory < 0 {
				node.UsedMemory = 0
			}
		}
	}
	t.AllocatedGPUs = []string{}
}

func (s *Store) AllocateGPU(gpuID, taskID string, memoryGB int, node *model.Node, cpuReq, memReq int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gpu := s.cluster.FindGPUByID(gpuID)
	if gpu == nil {
		return
	}
	gpu.AllocatedMemory += memoryGB
	gpu.TaskIDs = append(gpu.TaskIDs, taskID)
	if len(gpu.TaskIDs) > 1 {
		gpu.Status = model.GPUStatusShared
	} else {
		gpu.Status = model.GPUStatusAllocated
	}
	if node != nil {
		node.UsedCPU += cpuReq
		node.UsedMemory += memReq
	}
}

func (s *Store) SetNodeStatus(name string, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cluster.SetNodeStatus(name, status)
}

func (s *Store) GetAllTasks() []*model.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*model.Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		result = append(result, t)
	}
	return result
}

func (s *Store) GetTasksByStatus(status model.TaskStatus) []*model.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*model.Task
	for _, t := range s.tasks {
		if t.Status == status {
			result = append(result, t)
		}
	}
	return result
}

func (s *Store) GetRunningTasks() []*model.Task {
	return s.GetTasksByStatus(model.TaskStatusRunning)
}

func (s *Store) GetQueuedTasks() []*model.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*model.Task
	for _, t := range s.tasks {
		if t.Status == model.TaskStatusQueued || t.Status == model.TaskStatusSubmitted {
			result = append(result, t)
		}
	}
	return result
}

func (s *Store) SetTaskRetry(id string, retryCount int, nextRetryAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.RetryCount = retryCount
		t.NextRetryAt = &nextRetryAt
		t.Status = model.TaskStatusQueued
		t.QueueEnterAt = time.Now()
		t.AllocatedGPUs = []string{}
	}
}

func (s *Store) SetTaskPreempted(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Status = model.TaskStatusPreempted
		now := time.Now()
		t.FinishedAt = &now
		t.AllocatedGPUs = []string{}
	}
}

func (s *Store) RequeuePreemptedTask(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Status = model.TaskStatusQueued
		t.QueueEnterAt = time.Now()
		t.StartedAt = nil
		t.FinishedAt = nil
		t.AllocatedGPUs = []string{}
	}
}

func (s *Store) SetGangWaitStart(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		now := time.Now()
		t.GangWaitStart = &now
	}
}

func (s *Store) AddUserUsage(user string, gpuMinutes float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userUsage[user] += gpuMinutes
}

func (s *Store) GetUserUsage(user string) float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userUsage[user]
}

func (s *Store) IsOverQuota(user string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	quota, ok := s.schedConfig.UserQuotas[user]
	if !ok {
		return false
	}
	return s.userUsage[user] > quota
}

func (s *Store) GetTaskBySpecName(name string) *model.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tasks {
		if t.Spec.Name == name {
			return t
		}
	}
	return nil
}

func (s *Store) FindTaskByID(id string) *model.Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tasks[id]
}

func (s *Store) UpdateTaskPriority(id string, newPriority int) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return 0, false
	}
	oldPriority := t.Spec.Priority
	if newPriority < 1 {
		newPriority = 1
	}
	if newPriority > 10 {
		newPriority = 10
	}
	t.Spec.Priority = newPriority
	return oldPriority, true
}

func (s *Store) AddTaskWithDeps(spec *model.TaskSpec) (*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, depID := range spec.DependsOn {
		if _, ok := s.tasks[depID]; !ok {
			return nil, fmt.Errorf("dependency task %s does not exist", depID)
		}
	}

	s.taskCounter++
	task := &model.Task{
		ID:            fmt.Sprintf("task-%04d", s.taskCounter),
		Spec:          *spec,
		SubmittedAt:   time.Now(),
		QueueEnterAt:  time.Now(),
		AllocatedGPUs: []string{},
	}

	if len(spec.DependsOn) > 0 {
		task.Status = model.TaskStatusBlocked
	} else {
		task.Status = model.TaskStatusSubmitted
	}

	s.tasks[task.ID] = task

	for _, depID := range spec.DependsOn {
		s.depGraph.AddEdge(task.ID, depID)
	}

	return task, nil
}

func (s *Store) AddTaskWithDepsBatch(specs []*model.TaskSpec) ([]*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, spec := range specs {
		for _, depID := range spec.DependsOn {
			if _, ok := s.tasks[depID]; !ok {
				found := false
				for _, otherSpec := range specs {
					if otherSpec.Name == depID {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("dependency task %s does not exist", depID)
				}
			}
		}
	}

	tempGraph := s.depGraph.Copy()

	var tasks []*model.Task
	nameToID := make(map[string]string)

	for _, spec := range specs {
		s.taskCounter++
		task := &model.Task{
			ID:            fmt.Sprintf("task-%04d", s.taskCounter),
			Spec:          *spec,
			SubmittedAt:   time.Now(),
			QueueEnterAt:  time.Now(),
			AllocatedGPUs: []string{},
		}
		nameToID[spec.Name] = task.ID
		tasks = append(tasks, task)
	}

	for _, task := range tasks {
		resolvedDeps := make([]string, 0, len(task.Spec.DependsOn))
		for _, depName := range task.Spec.DependsOn {
			if id, ok := nameToID[depName]; ok {
				resolvedDeps = append(resolvedDeps, id)
			} else {
				resolvedDeps = append(resolvedDeps, depName)
			}
		}
		task.Spec.DependsOn = resolvedDeps

		if len(resolvedDeps) > 0 {
			task.Status = model.TaskStatusBlocked
		} else {
			task.Status = model.TaskStatusSubmitted
		}

		for _, depID := range resolvedDeps {
			tempGraph.AddEdge(task.ID, depID)
		}
	}

	if cyclePath, hasCycle := dag.DetectCycle(tempGraph); hasCycle {
		return nil, fmt.Errorf("dependency cycle detected: %s", formatCyclePath(cyclePath))
	}

	for _, task := range tasks {
		s.tasks[task.ID] = task
	}

	s.depGraph = tempGraph

	return tasks, nil
}

func formatCyclePath(path []string) string {
	return stringsJoin(path, " → ")
}

func stringsJoin(elems []string, sep string) string {
	result := ""
	for i, e := range elems {
		if i > 0 {
			result += sep
		}
		result += e
	}
	return result
}

func (s *Store) GetDepGraph() *dag.DependencyGraph {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.depGraph
}

func (s *Store) AreDependenciesSatisfied(taskID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	deps := s.depGraph.Dependencies(taskID)
	for _, depID := range deps {
		t, ok := s.tasks[depID]
		if !ok || t.Status != model.TaskStatusCompleted {
			return false
		}
	}
	return true
}

func (s *Store) HasFailedDependency(taskID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	deps := s.depGraph.Dependencies(taskID)
	for _, depID := range deps {
		t, ok := s.tasks[depID]
		if !ok {
			return true
		}
		if t.Status == model.TaskStatusFailed || t.Status == model.TaskStatusCancelled ||
			t.Status == model.TaskStatusSkipped {
			return true
		}
	}
	return false
}

func (s *Store) GetBlockedTasks() []*model.Task {
	return s.GetTasksByStatus(model.TaskStatusBlocked)
}

func (s *Store) SetDepGraph(g *dag.DependencyGraph) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.depGraph = g
}

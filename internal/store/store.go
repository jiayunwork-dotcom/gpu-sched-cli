package store

import (
	"fmt"
	"sync"
	"time"

	"github.com/gpu-sched-cli/internal/dag"
	"github.com/gpu-sched-cli/internal/model"
)

type Store struct {
	mu              sync.RWMutex
	cluster         *model.Cluster
	tasks           map[string]*model.Task
	schedConfig     *model.SchedulerConfig
	userUsage       map[string]float64
	taskCounter     int
	audit           *AuditLogger
	depGraph        *dag.DependencyGraph
	depTimeoutTrack *dag.DepTimeoutTracker
}

func NewStore(cluster *model.Cluster, config *model.SchedulerConfig) *Store {
	return &Store{
		cluster:         cluster,
		tasks:           make(map[string]*model.Task),
		schedConfig:     config,
		userUsage:       make(map[string]float64),
		audit:           NewAuditLogger(),
		depGraph:        dag.NewDependencyGraph(),
		depTimeoutTrack: dag.NewDepTimeoutTracker(),
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

	for _, dep := range spec.DependsOn {
		if _, ok := s.tasks[dep.Task]; !ok {
			return nil, fmt.Errorf("dependency task %s does not exist", dep.Task)
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

	for _, dep := range spec.DependsOn {
		condition := dag.DepCondition(dep.Condition)
		if condition == "" {
			condition = dag.DepConditionCompleted
		}
		s.depGraph.AddEdgeWithOptions(task.ID, dep.Task, condition, dep.Weight, dep.Timeout)
	}

	return task, nil
}

func (s *Store) AddTaskWithDepsBatch(specs []*model.TaskSpec) ([]*model.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, spec := range specs {
		for _, dep := range spec.DependsOn {
			if _, ok := s.tasks[dep.Task]; !ok {
				found := false
				for _, otherSpec := range specs {
					if otherSpec.Name == dep.Task {
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("dependency task %s does not exist", dep.Task)
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
		resolvedDeps := make([]model.DependencySpec, 0, len(task.Spec.DependsOn))
		for _, dep := range task.Spec.DependsOn {
			depTask := dep.Task
			if id, ok := nameToID[depTask]; ok {
				depTask = id
			}
			resolvedDeps = append(resolvedDeps, model.DependencySpec{
				Task:      depTask,
				Condition: dep.Condition,
				Weight:    dep.Weight,
				Timeout:   dep.Timeout,
			})
		}
		task.Spec.DependsOn = resolvedDeps

		if len(resolvedDeps) > 0 {
			task.Status = model.TaskStatusBlocked
		} else {
			task.Status = model.TaskStatusSubmitted
		}

		for _, dep := range resolvedDeps {
			condition := dag.DepCondition(dep.Condition)
			if condition == "" {
				condition = dag.DepConditionCompleted
			}
			weight := dep.Weight
			if weight <= 0 {
				weight = 1
			}
			tempGraph.AddEdgeWithOptions(task.ID, dep.Task, condition, weight, dep.Timeout)
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
	return s.areDependenciesSatisfiedLocked(taskID)
}

func (s *Store) areDependenciesSatisfiedLocked(taskID string) bool {
	edges := s.depGraph.DependencyEdges(taskID)
	if len(edges) == 0 {
		return true
	}
	for _, edge := range edges {
		if s.depTimeoutTrack.IsTimedOut(taskID, edge.To) {
			continue
		}
		if !dag.IsDependencySatisfied(edge, func(id string) *model.Task {
			return s.tasks[id]
		}) {
			return false
		}
	}
	return true
}

func (s *Store) HasFailedDependency(taskID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	edges := s.depGraph.DependencyEdges(taskID)
	for _, edge := range edges {
		if s.depTimeoutTrack.IsTimedOut(taskID, edge.To) {
			continue
		}
		if dag.IsDependencyFailed(edge, func(id string) *model.Task {
			return s.tasks[id]
		}) {
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

func (s *Store) AddDependency(taskID, depID string, condition model.DepCondition, weight int, timeout int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	if _, ok := s.tasks[depID]; !ok {
		return fmt.Errorf("dependency task %s not found", depID)
	}

	if task.Status != model.TaskStatusBlocked && task.Status != model.TaskStatusQueued &&
		task.Status != model.TaskStatusSubmitted {
		return fmt.Errorf("cannot add dependency: task %s is in state %s (only blocked/queued/submitted allowed)",
			taskID, task.Status)
	}

	tempGraph := s.depGraph.Copy()
	dagCond := dag.DepCondition(condition)
	if dagCond == "" {
		dagCond = dag.DepConditionCompleted
	}
	if weight <= 0 {
		weight = 1
	}
	tempGraph.AddEdgeWithOptions(taskID, depID, dagCond, weight, timeout)

	if cyclePath, hasCycle := dag.DetectCycle(tempGraph); hasCycle {
		return fmt.Errorf("cannot add dependency: would create cycle: %s", formatCyclePath(cyclePath))
	}

	s.depGraph = tempGraph

	task.Spec.DependsOn = append(task.Spec.DependsOn, model.DependencySpec{
		Task:      depID,
		Condition: model.DepCondition(dagCond),
		Weight:    weight,
		Timeout:   timeout,
	})

	var becameBlocked bool
	if task.Status == model.TaskStatusQueued || task.Status == model.TaskStatusSubmitted {
		task.Status = model.TaskStatusBlocked
		becameBlocked = true
	}

	s.audit.Record(model.AuditDecisionDepAdd, taskID, nil,
		fmt.Sprintf("添加依赖: %s -> %s (condition=%s, weight=%d, timeout=%d)", taskID, depID, dagCond, weight, timeout),
		map[string]string{
			"dependency": depID,
			"condition":  string(dagCond),
			"weight":     fmt.Sprintf("%d", weight),
			"timeout_min": fmt.Sprintf("%d", timeout),
			"became_blocked": fmt.Sprintf("%t", becameBlocked),
		})

	return nil
}

func (s *Store) RemoveDependency(taskID, depID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[taskID]
	if !ok {
		return false, fmt.Errorf("task %s not found", taskID)
	}

	if task.Status != model.TaskStatusBlocked && task.Status != model.TaskStatusQueued &&
		task.Status != model.TaskStatusSubmitted {
		return false, fmt.Errorf("cannot remove dependency: task %s is in state %s (only blocked/queued/submitted allowed)",
			taskID, task.Status)
	}

	var removedEdge *dag.DependencyEdge
	edges := s.depGraph.DependencyEdges(taskID)
	for _, e := range edges {
		if e.To == depID {
			removedEdge = e
			break
		}
	}

	removed := s.depGraph.RemoveEdge(taskID, depID)
	if !removed {
		return false, nil
	}

	newDeps := make([]model.DependencySpec, 0, len(task.Spec.DependsOn))
	for _, d := range task.Spec.DependsOn {
		if d.Task != depID {
			newDeps = append(newDeps, d)
		}
	}
	task.Spec.DependsOn = newDeps

	var becameUnblocked bool
	if s.areDependenciesSatisfiedLocked(taskID) && task.Status == model.TaskStatusBlocked {
		task.Status = model.TaskStatusQueued
		task.QueueEnterAt = time.Now()
		becameUnblocked = true
	}

	condition := "completed"
	weight := 1
	timeout := 0
	if removedEdge != nil {
		condition = string(removedEdge.Condition)
		weight = removedEdge.Weight
		timeout = removedEdge.Timeout
	}
	s.audit.Record(model.AuditDecisionDepRemove, taskID, nil,
		fmt.Sprintf("移除依赖: %s -> %s (condition=%s, weight=%d, timeout=%d)", taskID, depID, condition, weight, timeout),
		map[string]string{
			"dependency":       depID,
			"condition":        condition,
			"weight":           fmt.Sprintf("%d", weight),
			"timeout_min":      fmt.Sprintf("%d", timeout),
			"became_unblocked": fmt.Sprintf("%t", becameUnblocked),
		})

	return true, nil
}

func (s *Store) CheckDepTimeouts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var newlyUnblocked []string
	now := time.Now()

	blockedTasks := s.getBlockedTasksLocked()
	for _, t := range blockedTasks {
		edges := s.depGraph.DependencyEdges(t.ID)
		for _, edge := range edges {
			if edge.Timeout <= 0 {
				continue
			}
			if s.depTimeoutTrack.IsTimedOut(t.ID, edge.To) {
				continue
			}
			if dag.IsDependencySatisfied(edge, func(id string) *model.Task {
				return s.tasks[id]
			}) {
				continue
			}
			depTask := s.tasks[edge.To]
			if depTask == nil {
				continue
			}
			var startTime time.Time
			if depTask.StartedAt != nil {
				startTime = *depTask.StartedAt
			} else {
				startTime = depTask.SubmittedAt
			}
			elapsed := now.Sub(startTime)
			timeoutDur := time.Duration(edge.Timeout) * time.Minute
			if elapsed >= timeoutDur {
				s.depTimeoutTrack.MarkTimedOut(t.ID, edge.To)
				s.audit.Record(model.AuditDecisionDepTimeout, t.ID, nil,
					fmt.Sprintf("依赖超时: 等待 %s 超过 %d 分钟", edge.To, edge.Timeout),
					map[string]string{
						"dependency": edge.To,
						"timeout_min": fmt.Sprintf("%d", edge.Timeout),
					})
			}
		}
		if s.areDependenciesSatisfiedLocked(t.ID) && t.Status == model.TaskStatusBlocked {
			t.Status = model.TaskStatusQueued
			t.QueueEnterAt = now
			newlyUnblocked = append(newlyUnblocked, t.ID)
		}
	}

	return newlyUnblocked
}

func (s *Store) getBlockedTasksLocked() []*model.Task {
	var result []*model.Task
	for _, t := range s.tasks {
		if t.Status == model.TaskStatusBlocked {
			result = append(result, t)
		}
	}
	return result
}

func (s *Store) GetDepTimeoutTracker() *dag.DepTimeoutTracker {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.depTimeoutTrack
}

func (s *Store) HasRunningTasks() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tasks {
		if t.Status == model.TaskStatusRunning || t.Status == model.TaskStatusScheduling {
			return true
		}
	}
	return false
}

func (s *Store) ReplaceDepGraph(newGraph *dag.DependencyGraph) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, t := range s.tasks {
		if t.Status == model.TaskStatusRunning || t.Status == model.TaskStatusScheduling {
			return fmt.Errorf("cannot replace dependency graph while tasks are running")
		}
	}

	s.depGraph = newGraph
	s.depTimeoutTrack = dag.NewDepTimeoutTracker()

	for taskID := range s.tasks {
		edges := newGraph.DependencyEdges(taskID)
		deps := make([]model.DependencySpec, len(edges))
		for i, e := range edges {
			deps[i] = model.DependencySpec{
				Task:      e.To,
				Condition: model.DepCondition(e.Condition),
				Weight:    e.Weight,
				Timeout:   e.Timeout,
			}
		}
		if t, ok := s.tasks[taskID]; ok {
			t.Spec.DependsOn = deps
			if len(deps) > 0 && t.Status != model.TaskStatusBlocked &&
				t.Status != model.TaskStatusRunning && t.Status != model.TaskStatusCompleted &&
				t.Status != model.TaskStatusFailed && t.Status != model.TaskStatusSkipped &&
				t.Status != model.TaskStatusCancelled && t.Status != model.TaskStatusTimedOut &&
				t.Status != model.TaskStatusPreempted {
				t.Status = model.TaskStatusBlocked
			}
			if len(deps) == 0 && t.Status == model.TaskStatusBlocked {
				t.Status = model.TaskStatusQueued
				t.QueueEnterAt = time.Now()
			}
		}
	}

	return nil
}

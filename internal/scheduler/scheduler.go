package scheduler

import (
	"fmt"
	"sort"
	"time"

	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/queue"
	"github.com/gpu-sched-cli/internal/store"
)

type Scheduler struct {
	store   *store.Store
	pq      *queue.PriorityQueue
	fairMgr *queue.FairShareManager
	borrowMgr *queue.BorrowManager
	stopCh  chan struct{}
}

func NewScheduler(s *store.Store, pq *queue.PriorityQueue, fm *queue.FairShareManager, bm *queue.BorrowManager) *Scheduler {
	return &Scheduler{
		store:     s,
		pq:        pq,
		fairMgr:   fm,
		borrowMgr: bm,
		stopCh:    make(chan struct{}),
	}
}

func (s *Scheduler) Start() {
	go s.fairMgr.StartAccountingLoop(s.stopCh)
	go s.runLoop()
}

func (s *Scheduler) Stop() {
	close(s.stopCh)
}

func (s *Scheduler) runLoop() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.Schedule()
		case <-s.stopCh:
			return
		}
	}
}

func (s *Scheduler) Schedule() {
	cfg := s.store.GetConfig()
	cluster := s.store.GetCluster()

	for {
		task := s.pq.Peek()
		if task == nil {
			return
		}

		task = s.pq.Dequeue()
		if task == nil {
			return
		}

		if task.Status != model.TaskStatusQueued && task.Status != model.TaskStatusSubmitted {
			continue
		}

		effectivePriority := s.fairMgr.GetEffectivePriority(task)

		allocated, crossNode := s.tryAllocate(task, cluster, cfg)
		if allocated {
			s.store.UpdateTaskStatus(task.ID, model.TaskStatusRunning)
			s.store.SetTaskAllocatedGPUs(task.ID, task.AllocatedGPUs, crossNode)
			s.pq.Remove(task.ID)
			continue
		}

		if cfg.PreemptEnabled && effectivePriority >= 8 {
			preempted := s.tryPreempt(task)
			if preempted {
				allocated2, crossNode2 := s.tryAllocate(task, cluster, cfg)
				if allocated2 {
					s.store.UpdateTaskStatus(task.ID, model.TaskStatusRunning)
					s.store.SetTaskAllocatedGPUs(task.ID, task.AllocatedGPUs, crossNode2)
					s.pq.Remove(task.ID)
					continue
				}
			}
		}

		s.store.UpdateTaskStatus(task.ID, model.TaskStatusQueued)
		if task.IsGangScheduling() && task.GangWaitStart == nil {
			s.store.SetGangWaitStart(task.ID)
		}
		s.pq.RequeueFront(task)
		break
	}
}

func (s *Scheduler) tryAllocate(task *model.Task, cluster *model.Cluster, cfg *model.SchedulerConfig) (bool, bool) {
	gpuCount := task.Spec.GPUReq.MaxCount
	if gpuCount <= 0 {
		gpuCount = task.Spec.GPUReq.MinCount
	}
	perGPUMem := task.Spec.GPUReq.MinMemory / task.Spec.GPUReq.MinCount

	if task.Spec.MultiCardComm {
		return s.allocateMultiCardWithTopology(task, cluster, cfg, gpuCount, perGPUMem)
	}

	nodes := s.getSortedNodes(cluster, cfg)
	for _, node := range nodes {
		if node.Status != "online" {
			continue
		}
		if node.AvailableCPU() < task.Spec.CPUReq {
			continue
		}
		if node.AvailableMemory() < task.Spec.MemoryReq {
			continue
		}

		if !s.checkAffinity(task, node) {
			continue
		}
		if !s.checkAntiAffinity(task, node) {
			continue
		}

		gpus := s.findGPUsOnNode(node, gpuCount, perGPUMem, task, cfg)
		if len(gpus) >= task.Spec.GPUReq.MinCount {
			actualCount := min(len(gpus), gpuCount)
			gpus = gpus[:actualCount]
			crossNode := false
			s.doAllocate(task, gpus, node, perGPUMem)
			return true, crossNode
		}
	}

	if cfg.SharingEnabled {
		for _, node := range nodes {
			if node.Status != "online" {
				continue
			}
			if node.AvailableCPU() < task.Spec.CPUReq {
				continue
			}
			if node.AvailableMemory() < task.Spec.MemoryReq {
				continue
			}
			if !s.checkAffinity(task, node) {
				continue
			}
			if !s.checkAntiAffinity(task, node) {
				continue
			}
			gpus := s.findSharedGPUsOnNode(node, gpuCount, perGPUMem, task)
			if len(gpus) >= task.Spec.GPUReq.MinCount {
				actualCount := min(len(gpus), gpuCount)
				gpus = gpus[:actualCount]
				s.doAllocate(task, gpus, node, perGPUMem)
				return true, false
			}
		}
	}

	return false, false
}

func (s *Scheduler) allocateMultiCardWithTopology(task *model.Task, cluster *model.Cluster, cfg *model.SchedulerConfig, gpuCount, perGPUMem int) (bool, bool) {
	nodes := s.getSortedNodes(cluster, cfg)

	for _, node := range nodes {
		if node.Status != "online" {
			continue
		}
		if node.AvailableCPU() < task.Spec.CPUReq {
			continue
		}
		if node.AvailableMemory() < task.Spec.MemoryReq {
			continue
		}
		if !s.checkAffinity(task, node) {
			continue
		}
		if !s.checkAntiAffinity(task, node) {
			continue
		}

		nvlinkGPUs := s.findNVLinkDomainGPUs(node, gpuCount, perGPUMem, task)
		if len(nvlinkGPUs) >= task.Spec.GPUReq.MinCount {
			actualCount := min(len(nvlinkGPUs), gpuCount)
			nvlinkGPUs = nvlinkGPUs[:actualCount]
			s.doAllocate(task, nvlinkGPUs, node, perGPUMem)
			return true, false
		}
	}

	for _, node := range nodes {
		if node.Status != "online" {
			continue
		}
		if node.AvailableCPU() < task.Spec.CPUReq {
			continue
		}
		if node.AvailableMemory() < task.Spec.MemoryReq {
			continue
		}
		if !s.checkAffinity(task, node) {
			continue
		}
		if !s.checkAntiAffinity(task, node) {
			continue
		}

		nodeGPUs := s.findGPUsOnNode(node, gpuCount, perGPUMem, task, cfg)
		if len(nodeGPUs) >= task.Spec.GPUReq.MinCount {
			actualCount := min(len(nodeGPUs), gpuCount)
			nodeGPUs = nodeGPUs[:actualCount]
			s.doAllocate(task, nodeGPUs, node, perGPUMem)
			return true, false
		}
	}

	var allGPUs []*model.GPU
	gpuNodeMap := make(map[string]*model.Node)
	for _, node := range nodes {
		if node.Status != "online" {
			continue
		}
		for _, gpu := range node.GPUs {
			if gpu.CanAllocate(perGPUMem) && s.matchModel(gpu, task) {
				allGPUs = append(allGPUs, gpu)
				gpuNodeMap[gpu.ID] = node
			}
		}
	}

	if len(allGPUs) >= task.Spec.GPUReq.MinCount {
		actualCount := min(len(allGPUs), gpuCount)
		sort.Slice(allGPUs, func(i, j int) bool {
			return allGPUs[i].AvailableMemory() < allGPUs[j].AvailableMemory()
		})
		selected := allGPUs[:actualCount]
		crossNode := false
		firstNode := gpuNodeMap[selected[0].ID]
		for _, g := range selected[1:] {
			if gpuNodeMap[g.ID] != firstNode {
				crossNode = true
				break
			}
		}
		for _, g := range selected {
			n := gpuNodeMap[g.ID]
			s.store.AllocateGPU(g.ID, task.ID, perGPUMem, n, task.Spec.CPUReq/actualCount, task.Spec.MemoryReq/actualCount)
		}
		task.AllocatedGPUs = make([]string, 0, len(selected))
		for _, g := range selected {
			task.AllocatedGPUs = append(task.AllocatedGPUs, g.ID)
		}
		return true, crossNode
	}

	return false, false
}

func (s *Scheduler) doAllocate(task *model.Task, gpus []*model.GPU, node *model.Node, perGPUMem int) {
	task.AllocatedGPUs = make([]string, 0, len(gpus))
	for _, gpu := range gpus {
		s.store.AllocateGPU(gpu.ID, task.ID, perGPUMem, node, task.Spec.CPUReq/len(gpus), task.Spec.MemoryReq/len(gpus))
		task.AllocatedGPUs = append(task.AllocatedGPUs, gpu.ID)
	}
}

func (s *Scheduler) getSortedNodes(cluster *model.Cluster, cfg *model.SchedulerConfig) []*model.Node {
	nodes := make([]*model.Node, 0, len(cluster.Nodes))
	for _, n := range cluster.Nodes {
		nodes = append(nodes, n)
	}

	if cfg.Strategy == model.StrategyBestFit {
		sort.Slice(nodes, func(i, j int) bool {
			ri := float64(len(niUsedGPUs(nodes[i]))) / float64(len(nodes[i].GPUs))
			rj := float64(len(niUsedGPUs(nodes[j]))) / float64(len(nodes[j].GPUs))
			return ri > rj
		})
	}

	return nodes
}

func niUsedGPUs(n *model.Node) []*model.GPU {
	var used []*model.GPU
	for _, g := range n.GPUs {
		if g.Status == model.GPUStatusAllocated || g.Status == model.GPUStatusShared {
			used = append(used, g)
		}
	}
	return used
}

func (s *Scheduler) findGPUsOnNode(node *model.Node, count, perGPUMem int, task *model.Task, cfg *model.SchedulerConfig) []*model.GPU {
	var candidates []*model.GPU
	for _, gpu := range node.GPUs {
		if gpu.CanAllocate(perGPUMem) && s.matchModel(gpu, task) {
			candidates = append(candidates, gpu)
		}
	}

	if len(candidates) < count {
		return candidates
	}

	if cfg.Strategy == model.StrategyBestFit {
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].AvailableMemory() < candidates[j].AvailableMemory()
		})
	}

	if len(candidates) > count {
		candidates = candidates[:count]
	}
	return candidates
}

func (s *Scheduler) findSharedGPUsOnNode(node *model.Node, count, perGPUMem int, task *model.Task) []*model.GPU {
	var candidates []*model.GPU
	for _, gpu := range node.GPUs {
		if gpu.CanShare(perGPUMem) && s.matchModel(gpu, task) {
			candidates = append(candidates, gpu)
		}
	}
	if len(candidates) > count {
		candidates = candidates[:count]
	}
	return candidates
}

func (s *Scheduler) findNVLinkDomainGPUs(node *model.Node, count, perGPUMem int, task *model.Task) []*model.GPU {
	nvlinkMap := make(map[string][]string)
	for _, link := range node.NVLinks {
		nvlinkMap[link.GPU1ID] = append(nvlinkMap[link.GPU1ID], link.GPU2ID)
		nvlinkMap[link.GPU2ID] = append(nvlinkMap[link.GPU2ID], link.GPU1ID)
	}

	var bestGroup []*model.GPU
	bestScore := 0

	for _, gpu := range node.GPUs {
		if !gpu.CanAllocate(perGPUMem) || !s.matchModel(gpu, task) {
			continue
		}
		group := []*model.GPU{gpu}
		visited := map[string]bool{gpu.ID: true}

		queue := []string{gpu.ID}
		for len(queue) > 0 && len(group) < count {
			current := queue[0]
			queue = queue[1:]
			for _, neighborID := range nvlinkMap[current] {
				if visited[neighborID] {
					continue
				}
				visited[neighborID] = true
				neighbor := findGPUOnNode(node, neighborID)
				if neighbor != nil && neighbor.CanAllocate(perGPUMem) && s.matchModel(neighbor, task) {
					group = append(group, neighbor)
					queue = append(queue, neighborID)
				}
			}
		}

		if len(group) >= task.Spec.GPUReq.MinCount {
			score := scoreGroup(group, node, nvlinkMap)
			if score > bestScore {
				bestScore = score
				if len(group) > count {
					group = group[:count]
				}
				bestGroup = group
			}
		}
	}

	return bestGroup
}

func findGPUOnNode(node *model.Node, id string) *model.GPU {
	for _, g := range node.GPUs {
		if g.ID == id {
			return g
		}
	}
	return nil
}

func scoreGroup(gpus []*model.GPU, node *model.Node, nvlinkMap map[string][]string) int {
	score := 0
	for i := 0; i < len(gpus); i++ {
		for j := i + 1; j < len(gpus); j++ {
			if areNVLinked(gpus[i].ID, gpus[j].ID, nvlinkMap) {
				score += 100
			} else if gpus[i].NodeName == gpus[j].NodeName {
				score += 60
			} else {
				score += 20
			}
		}
	}
	return score
}

func areNVLinked(id1, id2 string, nvlinkMap map[string][]string) bool {
	neighbors, ok := nvlinkMap[id1]
	if !ok {
		return false
	}
	for _, n := range neighbors {
		if n == id2 {
			return true
		}
	}
	return false
}

func (s *Scheduler) matchModel(gpu *model.GPU, task *model.Task) bool {
	if task.Spec.GPUReq.PreferModel == "" {
		return true
	}
	return gpu.Model == task.Spec.GPUReq.PreferModel
}

func (s *Scheduler) checkAffinity(task *model.Task, node *model.Node) bool {
	if task.Spec.Affinity == "" {
		return true
	}
	affTask := s.store.GetTaskBySpecName(task.Spec.Affinity)
	if affTask == nil {
		return true
	}
	for _, gpuID := range affTask.AllocatedGPUs {
		gpu := s.store.GetCluster().FindGPUByID(gpuID)
		if gpu != nil && gpu.NodeName == node.Name {
			return true
		}
	}
	return false
}

func (s *Scheduler) checkAntiAffinity(task *model.Task, node *model.Node) bool {
	if task.Spec.AntiAffinity == "" {
		return true
	}
	antiTask := s.store.GetTaskBySpecName(task.Spec.AntiAffinity)
	if antiTask == nil {
		return true
	}
	for _, gpuID := range antiTask.AllocatedGPUs {
		gpu := s.store.GetCluster().FindGPUByID(gpuID)
		if gpu != nil && gpu.NodeName == node.Name {
			return false
		}
	}
	return true
}

func (s *Scheduler) tryPreempt(task *model.Task) bool {
	runningTasks := s.store.GetRunningTasks()
	sort.Slice(runningTasks, func(i, j int) bool {
		return runningTasks[i].Spec.Priority < runningTasks[j].Spec.Priority
	})

	for _, rt := range runningTasks {
		if rt.Spec.User == task.Spec.User {
			continue
		}
		if rt.Spec.Priority > 3 {
			continue
		}
		if task.Spec.Priority < 8 {
			continue
		}

		s.store.ReleaseTaskGPUs(rt.ID)
		s.store.SetTaskPreempted(rt.ID)
		s.store.RequeuePreemptedTask(rt.ID)
		s.pq.RequeueFront(rt)
		return true
	}
	return false
}

func (s *Scheduler) EstimateWaitTime(task *model.Task) time.Duration {
	h, m, l := s.pq.Depth()
	totalInQueue := h + m + l
	if totalInQueue == 0 {
		return 0
	}
	avgRuntime := 30 * time.Minute
	effectivePriority := s.fairMgr.GetEffectivePriority(task)
	position := 0
	switch {
	case effectivePriority >= 8:
		position = 0
	case effectivePriority >= 4:
		position = h
	default:
		position = h + m
	}
	return time.Duration(position) * avgRuntime
}

func (s *Scheduler) HandleGangTimeout() {
	cfg := s.store.GetConfig()
	queuedTasks := s.store.GetQueuedTasks()
	for _, t := range queuedTasks {
		if t.GangWaitStart == nil {
			continue
		}
		elapsed := time.Since(*t.GangWaitStart)
		if elapsed > time.Duration(cfg.GangWaitTimeoutMin)*time.Minute {
			if t.Spec.GPUReq.MaxCount > t.Spec.GPUReq.MinCount {
				t.Spec.GPUReq.MaxCount = t.Spec.GPUReq.MinCount
				s.store.SetGangWaitStart(t.ID)
				fmt.Printf("[gang-timeout] Task %s downgraded to min GPU count %d\n", t.ID, t.Spec.GPUReq.MinCount)
			} else {
				s.pq.Remove(t.ID)
				s.store.UpdateTaskStatus(t.ID, model.TaskStatusQueued)
				s.pq.RequeueFront(t)
				s.store.SetGangWaitStart(t.ID)
				fmt.Printf("[gang-timeout] Task %s re-queued, cannot satisfy min GPU count %d\n", t.ID, t.Spec.GPUReq.MinCount)
			}
		}
	}
}

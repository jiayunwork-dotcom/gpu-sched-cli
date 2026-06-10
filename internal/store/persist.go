package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gpu-sched-cli/internal/dag"
	"github.com/gpu-sched-cli/internal/model"
)

const defaultStateFile = ".gpu-sched-state.json"

type PersistedDepEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
	Weight    int    `json:"weight,omitempty"`
	Timeout   int    `json:"timeout,omitempty"`
}

type PersistedState struct {
	Tasks           map[string]*model.Task       `json:"tasks"`
	TaskCounter     int                           `json:"task_counter"`
	UserUsage       map[string]float64            `json:"user_usage"`
	NodeStatus      map[string]string             `json:"node_status"`
	GPUAllocs       map[string]*GPUAlloc          `json:"gpu_allocs"`
	SchedulerConfig *model.SchedulerConfig        `json:"scheduler_config,omitempty"`
	AuditRecords    []*model.AuditRecord          `json:"audit_records,omitempty"`
	DepEdges        []PersistedDepEdge            `json:"dep_edges,omitempty"`
	DepEdgesOld     map[string][]string           `json:"-"`
}

type GPUAlloc struct {
	AllocatedMemory int      `json:"allocated_memory"`
	TaskIDs         []string `json:"task_ids"`
	Status          string   `json:"status"`
}

type StateManager struct {
	mu        sync.Mutex
	stateFile string
	store     *Store
}

func NewStateManager(s *Store) *StateManager {
	home, _ := os.UserHomeDir()
	stateFile := filepath.Join(home, defaultStateFile)
	return &StateManager{
		stateFile: stateFile,
		store:     s,
	}
}

func (sm *StateManager) SetStateFile(path string) {
	if path != "" {
		sm.stateFile = path
	}
}

func (sm *StateManager) Save() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	cluster := sm.store.GetCluster()
	tasks := sm.store.GetAllTasks()

	gpuAllocs := make(map[string]*GPUAlloc)
	for _, node := range cluster.Nodes {
		for _, gpu := range node.GPUs {
			if gpu.Status != model.GPUStatusFree {
				gpuAllocs[gpu.ID] = &GPUAlloc{
					AllocatedMemory: gpu.AllocatedMemory,
					TaskIDs:         gpu.TaskIDs,
					Status:          string(gpu.Status),
				}
			}
		}
	}

	nodeStatus := make(map[string]string)
	for _, node := range cluster.Nodes {
		nodeStatus[node.Name] = node.Status
	}

	taskMap := make(map[string]*model.Task)
	for _, t := range tasks {
		taskMap[t.ID] = t
	}

	depGraph := sm.store.depGraph
	var depEdges []PersistedDepEdge
	if depGraph != nil {
		for from, edges := range depGraph.AllNodes() {
			for _, e := range edges {
				depEdges = append(depEdges, PersistedDepEdge{
					From:      from,
					To:        e.To,
					Condition: string(e.Condition),
					Weight:    e.Weight,
					Timeout:   e.Timeout,
				})
			}
		}
	}

	state := &PersistedState{
		Tasks:           taskMap,
		TaskCounter:     sm.store.taskCounter,
		UserUsage:       sm.store.userUsage,
		NodeStatus:      nodeStatus,
		GPUAllocs:       gpuAllocs,
		SchedulerConfig: sm.store.schedConfig,
		AuditRecords:    sm.store.audit.AllRecords(),
		DepEdges:        depEdges,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmpFile := sm.stateFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpFile, sm.stateFile)
}

func (sm *StateManager) Load() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	data, err := os.ReadFile(sm.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var state PersistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	sm.store.mu.Lock()
	defer sm.store.mu.Unlock()

	sm.store.taskCounter = state.TaskCounter
	sm.store.userUsage = state.UserUsage
	if sm.store.userUsage == nil {
		sm.store.userUsage = make(map[string]float64)
	}

	for id, t := range state.Tasks {
		sm.store.tasks[id] = t
		if t.AllocatedGPUs == nil {
			t.AllocatedGPUs = []string{}
		}
	}

	for nodeName, status := range state.NodeStatus {
		if node, ok := sm.store.cluster.Nodes[nodeName]; ok {
			node.Status = status
			if status == "offline" {
				for _, gpu := range node.GPUs {
					gpu.Status = model.GPUStatusFault
				}
			}
		}
	}

	for gpuID, alloc := range state.GPUAllocs {
		gpu := sm.store.cluster.FindGPUByID(gpuID)
		if gpu != nil {
			gpu.AllocatedMemory = alloc.AllocatedMemory
			gpu.TaskIDs = alloc.TaskIDs
			if gpu.TaskIDs == nil {
				gpu.TaskIDs = []string{}
			}
			gpu.Status = model.GPUStatus(alloc.Status)
		}
	}

	now := time.Now()
	cfg := sm.store.schedConfig
	var tasksToRelease []string

	for _, t := range sm.store.tasks {
		if t.Status == model.TaskStatusRunning && t.StartedAt != nil {
			runtime := now.Sub(*t.StartedAt)
			if cfg.TimeoutEnabled && runtime > time.Duration(float64(t.Spec.EstimatedMin)*cfg.TimeoutMultiplier)*time.Minute {
				t.Status = model.TaskStatusTimedOut
				t.FinishedAt = &now
				tasksToRelease = append(tasksToRelease, t.ID)
			}
		}
	}

	for _, taskID := range tasksToRelease {
		sm.releaseTaskGPUsInternal(taskID)
	}

	for _, t := range sm.store.tasks {
		if t.Status != model.TaskStatusRunning && len(t.AllocatedGPUs) > 0 {
			sm.releaseTaskGPUsInternal(t.ID)
		}
	}

	if state.SchedulerConfig != nil {
		sm.store.schedConfig = state.SchedulerConfig
	}

	if len(state.AuditRecords) > 0 {
		sm.store.audit.SetRecords(state.AuditRecords)
	}

	if len(state.DepEdges) > 0 {
		g := dag.NewDependencyGraph()
		for _, e := range state.DepEdges {
			condition := dag.DepCondition(e.Condition)
			if condition == "" {
				condition = dag.DepConditionCompleted
			}
			weight := e.Weight
			if weight <= 0 {
				weight = 1
			}
			g.AddEdgeWithOptions(e.From, e.To, condition, weight, e.Timeout)
		}
		sm.store.depGraph = g
	}

	return nil
}

func (sm *StateManager) releaseTaskGPUsInternal(taskID string) {
	t, ok := sm.store.tasks[taskID]
	if !ok {
		return
	}
	for _, gpuID := range t.AllocatedGPUs {
		gpu := sm.store.cluster.FindGPUByID(gpuID)
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
			if tid != taskID {
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
		node := sm.store.cluster.Nodes[gpu.NodeName]
		if node != nil {
			node.UsedCPU -= t.Spec.CPUReq / len(t.AllocatedGPUs)
			node.UsedMemory -= t.Spec.MemoryReq / len(t.AllocatedGPUs)
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

func (sm *StateManager) StateFile() string {
	return sm.stateFile
}

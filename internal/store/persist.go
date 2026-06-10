package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gpu-sched-cli/internal/model"
)

const defaultStateFile = ".gpu-sched-state.json"

type PersistedState struct {
	Tasks       map[string]*model.Task `json:"tasks"`
	TaskCounter int                    `json:"task_counter"`
	UserUsage   map[string]float64     `json:"user_usage"`
	NodeStatus  map[string]string      `json:"node_status"`
	GPUAllocs   map[string]*GPUAlloc   `json:"gpu_allocs"`
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

	state := &PersistedState{
		Tasks:       taskMap,
		TaskCounter: sm.store.taskCounter,
		UserUsage:   sm.store.userUsage,
		NodeStatus:  nodeStatus,
		GPUAllocs:   gpuAllocs,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sm.stateFile, data, 0644)
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
	for _, t := range sm.store.tasks {
		if t.Status == model.TaskStatusRunning && t.StartedAt != nil {
			for _, gpuID := range t.AllocatedGPUs {
				gpu := sm.store.cluster.FindGPUByID(gpuID)
				if gpu != nil {
					node := sm.store.cluster.Nodes[gpu.NodeName]
					if node != nil {
						node.UsedCPU += t.Spec.CPUReq / len(t.AllocatedGPUs)
						node.UsedMemory += t.Spec.MemoryReq / len(t.AllocatedGPUs)
					}
				}
			}
			runtime := now.Sub(*t.StartedAt)
			cfg := sm.store.schedConfig
			if cfg.TimeoutEnabled && runtime > time.Duration(float64(t.Spec.EstimatedMin)*cfg.TimeoutMultiplier)*time.Minute {
				t.Status = model.TaskStatusTimedOut
				t.FinishedAt = &now
				t.AllocatedGPUs = []string{}
			}
		}
	}

	return nil
}

func (sm *StateManager) StateFile() string {
	return sm.stateFile
}

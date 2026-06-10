package queue

import (
	"sync"
	"time"

	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/store"
)

type FairShareManager struct {
	mu         sync.RWMutex
	store      *store.Store
	usage      map[string]float64
	usageTime  map[string]time.Time
	quotaCycle time.Duration
}

func NewFairShareManager(s *store.Store) *FairShareManager {
	return &FairShareManager{
		store:      s,
		usage:      make(map[string]float64),
		usageTime:  make(map[string]time.Time),
		quotaCycle: 24 * time.Hour,
	}
}

func (f *FairShareManager) RecordUsage(user string, gpuCount int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usage[user] += float64(gpuCount)
	f.usageTime[user] = time.Now()
	f.store.AddUserUsage(user, float64(gpuCount))
}

func (f *FairShareManager) GetUserUsage(user string) float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.usage[user]
}

func (f *FairShareManager) IsOverQuota(user string) bool {
	return f.store.IsOverQuota(user)
}

func (f *FairShareManager) GetEffectivePriority(task *model.Task) int {
	priority := task.Spec.Priority
	if f.IsOverQuota(task.Spec.User) {
		priority -= 2
		if priority < 1 {
			priority = 1
		}
	}
	return priority
}

func (f *FairShareManager) StartAccountingLoop(stopCh <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			f.accountRunningTasks()
		case <-stopCh:
			return
		}
	}
}

func (f *FairShareManager) accountRunningTasks() {
	runningTasks := f.store.GetRunningTasks()
	for _, t := range runningTasks {
		gpuCount := len(t.AllocatedGPUs)
		if gpuCount > 0 {
			f.RecordUsage(t.Spec.User, gpuCount)
		}
	}
}

type BorrowManager struct {
	mu       sync.RWMutex
	store    *store.Store
	queue    *PriorityQueue
	borrowed map[string]string
}

func NewBorrowManager(s *store.Store, q *PriorityQueue) *BorrowManager {
	return &BorrowManager{
		store:    s,
		queue:    q,
		borrowed: make(map[string]string),
	}
}

func (b *BorrowManager) TryBorrow(level int) *model.Task {
	b.mu.Lock()
	defer b.mu.Unlock()
	tasks := b.queue.GetBorrowableTasks(level)
	if len(tasks) > 0 {
		return tasks[0]
	}
	return nil
}

func (b *BorrowManager) RecordBorrow(taskID, fromQueue string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.borrowed[taskID] = fromQueue
}

func (b *BorrowManager) ReclaimForQueue(level int) []*model.Task {
	b.mu.Lock()
	defer b.mu.Unlock()
	var reclaimed []*model.Task
	for taskID, fromQueue := range b.borrowed {
		queueLevel := queueNameToLevel(fromQueue)
		if queueLevel == level {
			task := b.store.GetTask(taskID)
			if task != nil && task.Status == model.TaskStatusRunning {
				b.store.SetTaskPreempted(taskID)
				b.store.ReleaseTaskGPUs(taskID)
				task.Status = model.TaskStatusPreempted
				b.queue.RequeueFront(task)
				delete(b.borrowed, taskID)
				reclaimed = append(reclaimed, task)
			}
		}
	}
	return reclaimed
}

func queueNameToLevel(name string) int {
	switch name {
	case "high":
		return 0
	case "medium":
		return 1
	default:
		return 2
	}
}

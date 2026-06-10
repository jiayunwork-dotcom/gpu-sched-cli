package queue

import (
	"sort"
	"sync"
	"time"

	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/store"
)

type PriorityQueue struct {
	mu    sync.RWMutex
	high  []*model.Task
	med   []*model.Task
	low   []*model.Task
	store *store.Store
}

func NewPriorityQueue(s *store.Store) *PriorityQueue {
	return &PriorityQueue{
		store: s,
	}
}

func (q *PriorityQueue) Enqueue(task *model.Task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	effectivePriority := task.Spec.Priority
	if q.store.IsOverQuota(task.Spec.User) {
		effectivePriority -= 2
		if effectivePriority < 1 {
			effectivePriority = 1
		}
	}
	switch {
	case effectivePriority >= 8:
		q.high = append(q.high, task)
	case effectivePriority >= 4:
		q.med = append(q.med, task)
	default:
		q.low = append(q.low, task)
	}
}

func (q *PriorityQueue) Dequeue() *model.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.high) > 0 {
		task := q.high[0]
		q.high = q.high[1:]
		return task
	}
	if len(q.med) > 0 {
		task := q.med[0]
		q.med = q.med[1:]
		return task
	}
	if len(q.low) > 0 {
		task := q.low[0]
		q.low = q.low[1:]
		return task
	}
	return nil
}

func (q *PriorityQueue) Peek() *model.Task {
	q.mu.RLock()
	defer q.mu.RUnlock()
	for _, list := range []([]*model.Task){q.high, q.med, q.low} {
		if len(list) > 0 {
			return list[0]
		}
	}
	return nil
}

func (q *PriorityQueue) RequeueFront(task *model.Task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case task.Spec.Priority >= 8:
		q.high = append([]*model.Task{task}, q.high...)
	case task.Spec.Priority >= 4:
		q.med = append([]*model.Task{task}, q.med...)
	default:
		q.low = append([]*model.Task{task}, q.low...)
	}
}

func (q *PriorityQueue) Remove(taskID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.high = removeTask(q.high, taskID)
	q.med = removeTask(q.med, taskID)
	q.low = removeTask(q.low, taskID)
}

func removeTask(tasks []*model.Task, id string) []*model.Task {
	for i, t := range tasks {
		if t.ID == id {
			return append(tasks[:i], tasks[i+1:]...)
		}
	}
	return tasks
}

func (q *PriorityQueue) Depth() (int, int, int) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.high), len(q.med), len(q.low)
}

func (q *PriorityQueue) TotalDepth() int {
	h, m, l := q.Depth()
	return h + m + l
}

func (q *PriorityQueue) AllTasks() []*model.Task {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var result []*model.Task
	result = append(result, q.high...)
	result = append(result, q.med...)
	result = append(result, q.low...)
	return result
}

func (q *PriorityQueue) AverageWaitTime() time.Duration {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var total time.Duration
	var count int
	for _, list := range []([]*model.Task){q.high, q.med, q.low} {
		for _, t := range list {
			total += time.Since(t.QueueEnterAt)
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / time.Duration(count)
}

func (q *PriorityQueue) AverageWaitTimeByLevel(level int) time.Duration {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var list []*model.Task
	switch level {
	case 0:
		list = q.high
	case 1:
		list = q.med
	default:
		list = q.low
	}
	if len(list) == 0 {
		return 0
	}
	var total time.Duration
	for _, t := range list {
		total += time.Since(t.QueueEnterAt)
	}
	return total / time.Duration(len(list))
}

func (q *PriorityQueue) GetBorrowableTasks(fromLevel int) []*model.Task {
	q.mu.RLock()
	defer q.mu.RUnlock()
	var tasks []*model.Task
	all := q.AllTasks()
	sort.Slice(all, func(i, j int) bool {
		return all[i].Spec.Priority < all[j].Spec.Priority
	})
	for _, t := range all {
		if t.QueueLevel() != fromLevel {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

func (q *PriorityQueue) Reprioritize(taskID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var task *model.Task
	q.high = removeTaskAndCollect(q.high, taskID, &task)
	q.med = removeTaskAndCollect(q.med, taskID, &task)
	q.low = removeTaskAndCollect(q.low, taskID, &task)
	if task == nil {
		return
	}
	effectivePriority := task.Spec.Priority
	if q.store.IsOverQuota(task.Spec.User) {
		effectivePriority -= 2
		if effectivePriority < 1 {
			effectivePriority = 1
		}
	}
	switch {
	case effectivePriority >= 8:
		q.high = append([]*model.Task{task}, q.high...)
	case effectivePriority >= 4:
		q.med = append([]*model.Task{task}, q.med...)
	default:
		q.low = append([]*model.Task{task}, q.low...)
	}
}

func removeTaskAndCollect(tasks []*model.Task, id string, collected **model.Task) []*model.Task {
	for i, t := range tasks {
		if t.ID == id {
			*collected = t
			return append(tasks[:i], tasks[i+1:]...)
		}
	}
	return tasks
}

func (q *PriorityQueue) Contains(taskID string) bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	for _, list := range []([]*model.Task){q.high, q.med, q.low} {
		for _, t := range list {
			if t.ID == taskID {
				return true
			}
		}
	}
	return false
}

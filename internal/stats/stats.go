package stats

import (
	"fmt"
	"time"

	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/queue"
	"github.com/gpu-sched-cli/internal/store"
)

type UserGPUHours struct {
	User       string
	Hours24h   float64
	HoursTotal float64
}

type NodeUtilization struct {
	NodeName      string
	AvgUtil       float64
	SampleCount   int
}

type QueueStats struct {
	QueueName       string
	AvgWaitTime     time.Duration
	TasksPerHour    float64
	CompletedCount  int
}

type ShareStats struct {
	ShareRate      float64
	SharedCount    int
	TotalAllocCount int
}

type OverallStats struct {
	UserGPUHours     []UserGPUHours
	NodeUtilization  []NodeUtilization
	QueueStats       []QueueStats
	ShareStats       ShareStats
}

func ComputeStats(st *store.Store, pq *queue.PriorityQueue) *OverallStats {
	now := time.Now()
	since24h := now.Add(-24 * time.Hour)

	audit := st.GetAuditLogger()
	allRecords := audit.AllRecords()
	tasks := st.GetAllTasks()
	cluster := st.GetCluster()

	return &OverallStats{
		UserGPUHours:     computeUserGPUHours(tasks, since24h),
		NodeUtilization:  computeNodeUtilization(cluster, allRecords, tasks),
		QueueStats:       computeQueueStats(tasks, pq, since24h),
		ShareStats:       computeShareStats(allRecords),
	}
}

func computeUserGPUHours(tasks []*model.Task, since24h time.Time) []UserGPUHours {
	userHours := make(map[string]*UserGPUHours)

	for _, t := range tasks {
		if t.StartedAt == nil {
			continue
		}
		gpuCount := t.GPUCount
		if gpuCount == 0 {
			gpuCount = len(t.AllocatedGPUs)
		}
		if gpuCount == 0 {
			continue
		}

		runtime := t.Runtime()
		if runtime <= 0 {
			continue
		}

		gpuMinutes := float64(gpuCount) * runtime.Minutes()
		gpuHours := gpuMinutes / 60.0

		if _, ok := userHours[t.Spec.User]; !ok {
			userHours[t.Spec.User] = &UserGPUHours{User: t.Spec.User}
		}
		userHours[t.Spec.User].HoursTotal += gpuHours

		if t.StartedAt.After(since24h) || (t.FinishedAt != nil && t.FinishedAt.After(since24h)) {
			var effectiveRuntime time.Duration
			if t.StartedAt.After(since24h) {
				effectiveRuntime = runtime
			} else if t.FinishedAt != nil {
				effectiveRuntime = t.FinishedAt.Sub(since24h)
			} else {
				effectiveRuntime = time.Since(since24h)
			}
			if effectiveRuntime > runtime {
				effectiveRuntime = runtime
			}
			gpuMinutes24h := float64(gpuCount) * effectiveRuntime.Minutes()
			userHours[t.Spec.User].Hours24h += gpuMinutes24h / 60.0
		}
	}

	result := make([]UserGPUHours, 0, len(userHours))
	for _, u := range userHours {
		result = append(result, *u)
	}
	return result
}

func computeNodeUtilization(cluster *model.Cluster, records []*model.AuditRecord, tasks []*model.Task) []NodeUtilization {
	result := make([]NodeUtilization, 0, len(cluster.Nodes))

	for name, node := range cluster.Nodes {
		currentUtil := node.GPUUtilization()

		allocEvents := 0
		deallocEvents := 0
		for _, r := range records {
			if r.DecisionType == model.AuditDecisionAllocate || r.DecisionType == model.AuditDecisionShare {
				for _, gpuID := range r.GPUs {
					gpu := cluster.FindGPUByID(gpuID)
					if gpu != nil && gpu.NodeName == name {
						allocEvents++
					}
				}
			}
			if r.DecisionType == model.AuditDecisionPreempt {
				for _, gpuID := range r.GPUs {
					gpu := cluster.FindGPUByID(gpuID)
					if gpu != nil && gpu.NodeName == name {
						deallocEvents++
					}
				}
			}
		}

		sampleCount := allocEvents + deallocEvents
		avgUtil := currentUtil
		if sampleCount > 0 && len(node.GPUs) > 0 {
			activeEvents := float64(allocEvents-deallocEvents) / float64(sampleCount)
			weightedUtil := activeEvents * 100
			avgUtil = (currentUtil + weightedUtil) / 2
		}
		if avgUtil < 0 {
			avgUtil = 0
		}
		if avgUtil > 100 {
			avgUtil = 100
		}

		result = append(result, NodeUtilization{
			NodeName:    name,
			AvgUtil:     avgUtil,
			SampleCount: sampleCount,
		})
	}
	return result
}

func computeQueueStats(tasks []*model.Task, pq *queue.PriorityQueue, since24h time.Time) []QueueStats {
	queues := []string{"high", "medium", "low"}
	result := make([]QueueStats, 0, 3)

	for i, qName := range queues {
		var totalWait time.Duration
		var waitCount int
		var completedCount int

		for _, t := range tasks {
			if t.QueueName() != qName {
				continue
			}
			if t.StartedAt != nil {
				wait := t.StartedAt.Sub(t.QueueEnterAt)
				if wait > 0 {
					totalWait += wait
					waitCount++
				}
			}
			if (t.Status == model.TaskStatusCompleted || t.Status == model.TaskStatusFailed || t.Status == model.TaskStatusTimedOut) &&
				t.FinishedAt != nil && t.FinishedAt.After(since24h) {
				completedCount++
			}
		}

		avgWait := time.Duration(0)
		if waitCount > 0 {
			avgWait = totalWait / time.Duration(waitCount)
		}

		queuedAvgWait := pq.AverageWaitTimeByLevel(i)
		if queuedAvgWait > 0 {
			if avgWait == 0 {
				avgWait = queuedAvgWait
			} else {
				avgWait = (avgWait + queuedAvgWait) / 2
			}
		}

		tasksPerHour := float64(completedCount) / 24.0

		result = append(result, QueueStats{
			QueueName:      qName,
			AvgWaitTime:    avgWait,
			TasksPerHour:   tasksPerHour,
			CompletedCount: completedCount,
		})
	}
	return result
}

func computeShareStats(records []*model.AuditRecord) ShareStats {
	sharedCount := 0
	totalAllocCount := 0

	for _, r := range records {
		if r.DecisionType == model.AuditDecisionAllocate {
			totalAllocCount++
		} else if r.DecisionType == model.AuditDecisionShare {
			totalAllocCount++
			sharedCount++
		}
	}

	shareRate := 0.0
	if totalAllocCount > 0 {
		shareRate = float64(sharedCount) / float64(totalAllocCount) * 100
	}

	return ShareStats{
		ShareRate:      shareRate,
		SharedCount:    sharedCount,
		TotalAllocCount: totalAllocCount,
	}
}

func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, m)
}

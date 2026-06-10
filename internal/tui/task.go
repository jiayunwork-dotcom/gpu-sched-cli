package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/store"
)

var (
	statusColors = map[model.TaskStatus]lipgloss.Style{
		model.TaskStatusSubmitted:  dimStyle,
		model.TaskStatusQueued:     yellowStyle,
		model.TaskStatusScheduling: infoStyle,
		model.TaskStatusRunning:    greenStyle,
		model.TaskStatusCompleted:  lipgloss.NewStyle().Foreground(lipgloss.Color("#b8bb26")),
		model.TaskStatusFailed:     redStyle,
		model.TaskStatusTimedOut:   redStyle,
		model.TaskStatusPreempted:  lipgloss.NewStyle().Foreground(lipgloss.Color("#fe8019")),
	}
)

func RenderTaskStatus(s *store.Store, taskID string) string {
	task := s.GetTask(taskID)
	if task == nil {
		return redStyle.Render(fmt.Sprintf("Task %s not found", taskID))
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("Task Status: %s", task.ID)) + "\n\n")

	statusStyle, ok := statusColors[task.Status]
	if !ok {
		statusStyle = lipgloss.NewStyle()
	}

	detailT := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#504945"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return headerStyle
			}
			return lipgloss.NewStyle()
		}).
		Headers("Property", "Value")

	detailT.Row("Name", task.Spec.Name)
	detailT.Row("Status", statusStyle.Render(string(task.Status)))
	detailT.Row("User", task.Spec.User)
	detailT.Row("Priority", fmt.Sprintf("%d (%s)", task.Spec.Priority, task.QueueName()))
	detailT.Row("GPU Request", fmt.Sprintf("%d-%d x %dGB (pref: %s)", task.Spec.GPUReq.MinCount, task.Spec.GPUReq.MaxCount, task.Spec.GPUReq.MinMemory, task.Spec.GPUReq.PreferModel))
	detailT.Row("CPU / Memory", fmt.Sprintf("%d cores / %d GB", task.Spec.CPUReq, task.Spec.MemoryReq))
	detailT.Row("Est. Duration", fmt.Sprintf("%d min", task.Spec.EstimatedMin))
	detailT.Row("Multi-card Comm", fmt.Sprintf("%v", task.Spec.MultiCardComm))

	if task.Spec.Affinity != "" {
		detailT.Row("Affinity", greenStyle.Render(task.Spec.Affinity))
	}
	if task.Spec.AntiAffinity != "" {
		detailT.Row("Anti-Affinity", redStyle.Render(task.Spec.AntiAffinity))
	}

	gpuList := strings.Join(task.AllocatedGPUs, ", ")
	if gpuList == "" {
		gpuList = "-"
	}
	detailT.Row("Allocated GPUs", gpuList)

	if task.CrossNode {
		detailT.Row("Cross-Node", redStyle.Render("Yes - performance may degrade"))
	}

	runtime := task.Runtime()
	detailT.Row("Runtime", formatDuration(runtime))

	if task.StartedAt != nil {
		detailT.Row("Started At", task.StartedAt.Format("15:04:05"))
	}
	if task.FinishedAt != nil {
		detailT.Row("Finished At", task.FinishedAt.Format("15:04:05"))
	}
	if task.RetryCount > 0 {
		detailT.Row("Retries", fmt.Sprintf("%d", task.RetryCount))
	}
	if task.GangWaitStart != nil {
		detailT.Row("Gang Wait", yellowStyle.Render(formatDuration(task.Runtime())))
	}

	detailT.Row("Submitted At", task.SubmittedAt.Format("15:04:05 2006-01-02"))

	b.WriteString(detailT.Render())
	return b.String()
}

func RenderTaskList(s *store.Store) string {
	tasks := s.GetAllTasks()
	var b strings.Builder
	b.WriteString(titleStyle.Render("All Tasks") + "\n\n")

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#504945"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return headerStyle
			}
			return lipgloss.NewStyle()
		}).
		Headers("ID", "Name", "Status", "Priority", "User", "GPUs", "Runtime")

	for _, task := range tasks {
		statusStyle, ok := statusColors[task.Status]
		if !ok {
			statusStyle = lipgloss.NewStyle()
		}
		gpuInfo := fmt.Sprintf("%d", len(task.AllocatedGPUs))
		if task.CrossNode {
			gpuInfo += " (cross)"
		}
		t.Row(
			task.ID,
			task.Spec.Name,
			statusStyle.Render(string(task.Status)),
			fmt.Sprintf("%d", task.Spec.Priority),
			task.Spec.User,
			gpuInfo,
			formatDuration(task.Runtime()),
		)
	}

	b.WriteString(t.Render())
	return b.String()
}

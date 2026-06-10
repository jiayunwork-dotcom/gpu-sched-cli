package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/queue"
	"github.com/gpu-sched-cli/internal/store"
)

type tickMsg time.Time

func doTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type TopModel struct {
	store   *store.Store
	pq      *queue.PriorityQueue
	width   int
	height  int
	quit    bool
}

func NewTopModel(s *store.Store, pq *queue.PriorityQueue) TopModel {
	return TopModel{
		store: s,
		pq:    pq,
	}
}

func (m TopModel) Init() tea.Cmd {
	return doTick()
}

func (m TopModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tickMsg:
		return m, doTick()
	}
	return m, nil
}

func (m TopModel) View() string {
	if m.quit {
		return ""
	}

	var b strings.Builder
	cluster := m.store.GetCluster()
	cfg := m.store.GetConfig()

	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#83a598")).
		Bold(true).
		Background(lipgloss.Color("#1d2021")).
		Padding(0, 1)

	b.WriteString(headerStyle.Render(fmt.Sprintf(" GPU Cluster Monitor | %s | %s ", cfg.Strategy, time.Now().Format("15:04:05"))))
	b.WriteString("\n\n")

	totalGPUs := cluster.TotalGPUs()
	usedGPUs := cluster.UsedGPUs()
	gpuUtil := cluster.GPUUtilization()
	totalMem := cluster.TotalMemory()
	usedMem := cluster.UsedMemory()
	memUtil := float64(0)
	if totalMem > 0 {
		memUtil = float64(usedMem) / float64(totalMem) * 100
	}

	barWidth := 30
	gpuBar := renderBar(usedGPUs, totalGPUs, barWidth, colorByUtil(gpuUtil))
	memBar := renderBar(usedMem, totalMem, barWidth, colorByUtil(memUtil))

	h, mq, l := m.pq.Depth()

	b.WriteString(fmt.Sprintf("  GPU: %s  %.1f%% (%d/%d)\n", gpuBar, gpuUtil, usedGPUs, totalGPUs))
	b.WriteString(fmt.Sprintf("  MEM: %s  %.1f%% (%d/%d GB)\n", memBar, memUtil, usedMem, totalMem))
	b.WriteString(fmt.Sprintf("  Queue: %s / %s / %s = %d tasks\n",
		redStyle.Render(fmt.Sprintf("H:%d", h)),
		yellowStyle.Render(fmt.Sprintf("M:%d", mq)),
		greenStyle.Render(fmt.Sprintf("L:%d", l)),
		h+mq+l,
	))
	b.WriteString("\n")

	nodeT := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#504945"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return headerStyle
			}
			return lipgloss.NewStyle()
		}).
		Headers("Node", "GPU Util", "GPU Bar", "Mem", "Status")

	for _, node := range cluster.Nodes {
		util := node.GPUUtilization()
		used := 0
		for _, g := range node.GPUs {
			if g.Status == model.GPUStatusAllocated || g.Status == model.GPUStatusShared {
				used++
			}
		}
		bar := renderBar(used, len(node.GPUs), 15, colorByUtil(util))
		statusStr := "🟢"
		if node.Status != "online" {
			statusStr = "🔴"
		}
		nodeT.Row(node.Name, colorByUtil(util).Render(fmt.Sprintf("%.0f%%", util)), bar, fmt.Sprintf("%d/%d GB", node.AvailableMemory(), node.MemoryGB), statusStr)
	}
	b.WriteString(nodeT.Render())
	b.WriteString("\n\n")

	runningTasks := m.store.GetRunningTasks()
	b.WriteString(headerStyle.Render(fmt.Sprintf(" Running Tasks (%d) ", len(runningTasks))) + "\n")

	if len(runningTasks) > 0 {
		taskT := table.New().
			Border(lipgloss.NormalBorder()).
			BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#504945"))).
			StyleFunc(func(row, col int) lipgloss.Style {
				if row == 0 {
					return headerStyle
				}
				return lipgloss.NewStyle()
			}).
			Headers("ID", "Name", "User", "Priority", "GPUs", "Runtime", "Cross")

		for _, t := range runningTasks {
			crossStr := ""
			if t.CrossNode {
				crossStr = redStyle.Render("Yes")
			} else {
				crossStr = dimStyle.Render("No")
			}
			taskT.Row(
				t.ID,
				t.Spec.Name,
				t.Spec.User,
				fmt.Sprintf("%d", t.Spec.Priority),
				fmt.Sprintf("%d", len(t.AllocatedGPUs)),
				formatDuration(t.Runtime()),
				crossStr,
			)
		}
		b.WriteString(taskT.Render())
	}

	b.WriteString(dimStyle.Render("\n  Press q to quit"))
	return b.String()
}

func renderBar(used, total, width int, style lipgloss.Style) string {
	if total == 0 {
		return strings.Repeat(" ", width)
	}
	filled := int(float64(width) * float64(used) / float64(total))
	if filled > width {
		filled = width
	}
	bar := style.Render(strings.Repeat("█", filled)) + dimStyle.Render(strings.Repeat("░", width-filled))
	return bar
}

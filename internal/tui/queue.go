package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/gpu-sched-cli/internal/queue"
	"github.com/gpu-sched-cli/internal/store"
)

func RenderQueueStatus(pq *queue.PriorityQueue, s *store.Store) string {
	h, m, l := pq.Depth()
	avgWait := pq.AverageWaitTime()
	avgWaitH := pq.AverageWaitTimeByLevel(0)
	avgWaitM := pq.AverageWaitTimeByLevel(1)
	avgWaitL := pq.AverageWaitTimeByLevel(2)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Queue Status") + "\n\n")

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#504945"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return headerStyle
			}
			return lipgloss.NewStyle()
		}).
		Headers("Queue", "Depth", "Avg Wait Time", "Weight")

	cfg := s.GetConfig()
	weights := cfg.QueueWeights

	t.Row(
		redStyle.Render("High (8-10)"),
		fmt.Sprintf("%d", h),
		formatDuration(avgWaitH),
		fmt.Sprintf("%.1f", weights["high"]),
	)
	t.Row(
		yellowStyle.Render("Medium (4-7)"),
		fmt.Sprintf("%d", m),
		formatDuration(avgWaitM),
		fmt.Sprintf("%.1f", weights["medium"]),
	)
	t.Row(
		greenStyle.Render("Low (1-3)"),
		fmt.Sprintf("%d", l),
		formatDuration(avgWaitL),
		fmt.Sprintf("%.1f", weights["low"]),
	)

	b.WriteString(t.Render() + "\n")
	b.WriteString(fmt.Sprintf("\n%s  Total: %d tasks  |  Overall Avg Wait: %s\n",
		infoStyle.Render("Summary:"),
		h+m+l,
		formatDuration(avgWait),
	))

	return b.String()
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

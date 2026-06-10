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
	greenStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#2ecc71")).Bold(true)
	yellowStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#f1c40f")).Bold(true)
	redStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e74c3c")).Bold(true)
	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8ec07c")).Bold(true)
	titleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#83a598")).Bold(true).Underline(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#665c54"))
	infoStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#d3869b"))
)

func colorByUtil(pct float64) lipgloss.Style {
	switch {
	case pct < 50:
		return greenStyle
	case pct < 80:
		return yellowStyle
	default:
		return redStyle
	}
}

func RenderClusterStatus(s *store.Store) string {
	cluster := s.GetCluster()
	var b strings.Builder

	b.WriteString(titleStyle.Render("Cluster Status") + "\n\n")

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#504945"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return headerStyle
			}
			return lipgloss.NewStyle()
		}).
		Headers("Node", "Status", "GPUs Used/Total", "GPU Util", "CPU Free", "Mem Free", "Network")

	for _, node := range cluster.Nodes {
		usedGPUs := 0
		for _, g := range node.GPUs {
			if g.Status == model.GPUStatusAllocated || g.Status == model.GPUStatusShared {
				usedGPUs++
			}
		}
		totalGPUs := len(node.GPUs)
		util := node.GPUUtilization()
		utilStr := fmt.Sprintf("%.1f%%", util)
		utilCell := colorByUtil(util).Render(utilStr)

		statusStr := greenStyle.Render("online")
		if node.Status != "online" {
			statusStr = redStyle.Render(node.Status)
		}

		cpuFree := fmt.Sprintf("%d/%d", node.AvailableCPU(), node.CPUcores)
		memFree := fmt.Sprintf("%d/%d GB", node.AvailableMemory(), node.MemoryGB)

		t.Row(node.Name, statusStr, fmt.Sprintf("%d/%d", usedGPUs, totalGPUs), utilCell, cpuFree, memFree, fmt.Sprintf("%.1f Gbps", node.NetworkGbps))
	}

	b.WriteString(t.Render() + "\n")

	totalUtil := cluster.GPUUtilization()
	b.WriteString(fmt.Sprintf("\n%s  Total GPU Utilization: %s  |  Memory: %d/%d GB\n",
		infoStyle.Render("Summary:"),
		colorByUtil(totalUtil).Render(fmt.Sprintf("%.1f%%", totalUtil)),
		cluster.UsedMemory(),
		cluster.TotalMemory(),
	))

	return b.String()
}

func RenderGPUDetails(s *store.Store, nodeName string) string {
	cluster := s.GetCluster()
	node, ok := cluster.Nodes[nodeName]
	if !ok {
		return redStyle.Render(fmt.Sprintf("Node %s not found", nodeName))
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("GPU Details - %s", nodeName)) + "\n\n")

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#504945"))).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == 0 {
				return headerStyle
			}
			return lipgloss.NewStyle()
		}).
		Headers("GPU ID", "Model", "Memory", "Allocated", "Status", "Tasks")

	for _, g := range node.GPUs {
		statusStr := greenStyle.Render(string(g.Status))
		if g.Status == model.GPUStatusAllocated {
			statusStr = yellowStyle.Render(string(g.Status))
		} else if g.Status == model.GPUStatusShared {
			statusStr = infoStyle.Render(string(g.Status))
		} else if g.Status == model.GPUStatusFault {
			statusStr = redStyle.Render(string(g.Status))
		}
		memStr := fmt.Sprintf("%d/%d GB", g.AllocatedMemory, g.MemoryGB)
		taskStr := strings.Join(g.TaskIDs, ", ")
		if taskStr == "" {
			taskStr = "-"
		}
		t.Row(g.ID, string(g.Model), memStr, fmt.Sprintf("%d GB", g.AllocatedMemory), statusStr, taskStr)
	}

	b.WriteString(t.Render())
	return b.String()
}

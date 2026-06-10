package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gpu-sched-cli/internal/dag"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/stats"
	"github.com/spf13/cobra"
)

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show resource usage statistics",
	Run: func(cmd *cobra.Command, args []string) {
		s := stats.ComputeStats(globalStore, globalQueue)

		fmt.Println("=== GPU Scheduler Statistics ===")
		fmt.Println()

		fmt.Println("--- User GPU Hours Consumption ---")
		if len(s.UserGPUHours) == 0 {
			fmt.Println("  No user usage data yet.")
		} else {
			sort.Slice(s.UserGPUHours, func(i, j int) bool {
				return s.UserGPUHours[i].HoursTotal > s.UserGPUHours[j].HoursTotal
			})
			fmt.Printf("  %-20s %-20s %-20s\n", "USER", "LAST 24H (GPU-h)", "TOTAL (GPU-h)")
			fmt.Println("  " + strings.Repeat("-", 62))
			for _, u := range s.UserGPUHours {
				fmt.Printf("  %-20s %-20.2f %-20.2f\n", u.User, u.Hours24h, u.HoursTotal)
			}
		}
		fmt.Println()

		fmt.Println("--- Node Historical Average Utilization ---")
		if len(s.NodeUtilization) == 0 {
			fmt.Println("  No node data.")
		} else {
			sort.Slice(s.NodeUtilization, func(i, j int) bool {
				return s.NodeUtilization[i].NodeName < s.NodeUtilization[j].NodeName
			})
			fmt.Printf("  %-20s %-20s %-20s\n", "NODE", "AVG UTIL (%)", "SAMPLE COUNT")
			fmt.Println("  " + strings.Repeat("-", 62))
			for _, n := range s.NodeUtilization {
				fmt.Printf("  %-20s %-20.1f %-20d\n", n.NodeName, n.AvgUtil, n.SampleCount)
			}
		}
		fmt.Println()

		fmt.Println("--- Priority Queue Statistics ---")
		fmt.Printf("  %-15s %-25s %-20s %-20s\n", "QUEUE", "AVG WAIT TIME", "COMPLETED (24h)", "THROUGHPUT (/h)")
		fmt.Println("  " + strings.Repeat("-", 82))
		for _, q := range s.QueueStats {
			fmt.Printf("  %-15s %-25s %-20d %-20.2f\n",
				q.QueueName,
				stats.FormatDuration(q.AvgWaitTime),
				q.CompletedCount,
				q.TasksPerHour,
			)
		}
		fmt.Println()

		fmt.Println("--- GPU Sharing Statistics ---")
		fmt.Printf("  Total allocations:     %d\n", s.ShareStats.TotalAllocCount)
		fmt.Printf("  Shared allocations:    %d\n", s.ShareStats.SharedCount)
		fmt.Printf("  GPU sharing rate:      %.1f%%\n", s.ShareStats.ShareRate)
		fmt.Println()

		fmt.Println("--- DAG Statistics ---")
		graph := globalStore.GetDepGraph()
		if len(graph.AllNodes()) == 0 {
			fmt.Println("  No task dependencies.")
		} else {
			dagStats := dag.ComputeStats(graph, func(id string) *model.Task {
				return globalStore.GetTask(id)
			})
			fmt.Printf("  Blocked tasks:          %d\n", dagStats.BlockedCount)
			fmt.Printf("  Avg dependency depth:   %.1f\n", dagStats.AvgDependencyDepth)
			fmt.Printf("  Critical path weight:   %d\n", dagStats.CriticalPathLen)
			if len(dagStats.CriticalPath) > 0 {
				pathStr := ""
				for i, n := range dagStats.CriticalPath {
					if i > 0 {
						pathStr += " → "
					}
					t := globalStore.GetTask(n)
					if t != nil {
						pathStr += t.Spec.Name
					} else {
						pathStr += n
					}
				}
				fmt.Printf("  Critical path chain:    %s\n", pathStr)
			}
		}
		fmt.Println()
	},
}

func init() {
	rootCmd.AddCommand(statsCmd)
}

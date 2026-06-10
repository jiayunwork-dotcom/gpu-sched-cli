package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/gpu-sched-cli/internal/model"
)

var (
	historyTaskID   string
	historyDecision string
	historyN        int
)

var historyCmd = &cobra.Command{
	Use:   "history",
	Short: "Show scheduling audit history",
	Run: func(cmd *cobra.Command, args []string) {
		audit := globalStore.GetAuditLogger()
		var records []*model.AuditRecord

		if historyTaskID != "" || historyDecision != "" {
			records = audit.Filter(historyTaskID, model.AuditDecisionType(historyDecision), historyN)
		} else {
			records = audit.GetRecords(historyN)
		}

		if len(records) == 0 {
			fmt.Println("No audit records found.")
			return
		}

		fmt.Printf("%-25s %-15s %-12s %-30s %s\n", "TIMESTAMP", "DECISION", "TASK", "GPUS", "REASON")
		fmt.Println(strings.Repeat("-", 120))

		for _, r := range records {
			gpuStr := strings.Join(r.GPUs, ",")
			if gpuStr == "" {
				gpuStr = "-"
			}
			reason := r.Reason
			if len(r.Extra) > 0 {
				extras := make([]string, 0, len(r.Extra))
				for k, v := range r.Extra {
					extras = append(extras, fmt.Sprintf("%s=%s", k, v))
				}
				reason = fmt.Sprintf("%s [%s]", reason, strings.Join(extras, ", "))
			}
			fmt.Printf("%-25s %-15s %-12s %-30s %s\n",
				r.Timestamp.Format(time.RFC3339),
				r.DecisionType,
				r.TaskID,
				truncate(gpuStr, 30),
				reason,
			)
		}
		fmt.Printf("\nTotal %d records shown (latest first if filtered)\n", len(records))
	},
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func init() {
	historyCmd.Flags().StringVarP(&historyTaskID, "task", "t", "", "Filter by task ID")
	historyCmd.Flags().StringVarP(&historyDecision, "type", "d", "", "Filter by decision type (allocate/preempt/share/queue/downgrade/reprioritize)")
	historyCmd.Flags().IntVarP(&historyN, "count", "n", 50, "Number of records to show")
	rootCmd.AddCommand(historyCmd)
}

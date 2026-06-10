package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/gpu-sched-cli/internal/tui"
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Queue management commands",
}

var queueStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show queue status",
	Aliases: []string{"s"},
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(tui.RenderQueueStatus(globalQueue, globalStore))
	},
}

func init() {
	queueCmd.AddCommand(queueStatusCmd)
	rootCmd.AddCommand(queueCmd)
}

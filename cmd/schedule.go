package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Run one scheduling pass and allocate queued tasks",
	Run: func(cmd *cobra.Command, args []string) {
		initSchedulerBackground()
		fmt.Println("Running scheduler...")
		time.Sleep(4 * time.Second)
		saveState()

		tasks := globalStore.GetAllTasks()
		running := 0
		queued := 0
		for _, t := range tasks {
			switch t.Status {
			case "running":
				running++
			case "queued", "submitted":
				queued++
			}
		}
		fmt.Printf("Scheduling complete: %d running, %d queued\n", running, queued)
	},
}

func init() {
	rootCmd.AddCommand(scheduleCmd)
}

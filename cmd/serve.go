package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the scheduler daemon (runs continuously)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("GPU Scheduler daemon starting...")
		fmt.Printf("  Cluster config: %s\n", clusterFile)
		fmt.Printf("  Scheduler config: %s\n", schedulerFile)
		fmt.Printf("  State file: %s\n", stateMgr.StateFile())
		fmt.Println("\nScheduler is running. Press Ctrl+C to stop.")
		startServe()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/gpu-sched-cli/internal/tui"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Cluster management commands",
}

var clusterStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster GPU utilization status",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(tui.RenderClusterStatus(globalStore))
	},
}

var clusterUpdateCmd = &cobra.Command{
	Use:   "update <node>",
	Short: "Update node status (online/offline)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		nodeName := args[0]
		status, _ := cmd.Flags().GetString("status")
		if status == "" {
			fmt.Println("Error: --status flag is required")
			return
		}
		if status != "online" && status != "offline" {
			fmt.Println("Error: status must be 'online' or 'offline'")
			return
		}
		globalStore.SetNodeStatus(nodeName, status)
		saveState()
		fmt.Printf("Node %s status updated to %s\n", nodeName, status)
	},
}

var clusterGPUCmd = &cobra.Command{
	Use:   "gpu <node>",
	Short: "Show GPU details for a node",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(tui.RenderGPUDetails(globalStore, args[0]))
	},
}

func init() {
	clusterUpdateCmd.Flags().String("status", "", "Node status: online/offline")

	clusterCmd.AddCommand(clusterStatusCmd)
	clusterCmd.AddCommand(clusterUpdateCmd)
	clusterCmd.AddCommand(clusterGPUCmd)
	rootCmd.AddCommand(clusterCmd)
}

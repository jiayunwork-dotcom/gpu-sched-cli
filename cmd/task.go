package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/tui"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Task management commands",
}

var taskStatusCmd = &cobra.Command{
	Use:    "status <task-id>",
	Short:  "Show task status details",
	Args:   cobra.ExactArgs(1),
	Aliases: []string{"s"},
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(tui.RenderTaskStatus(globalStore, args[0]))
	},
}

var taskListCmd = &cobra.Command{
	Use:    "list",
	Short:  "List all tasks",
	Aliases: []string{"ls"},
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(tui.RenderTaskList(globalStore))
	},
}

var taskCompleteCmd = &cobra.Command{
	Use:   "complete <task-id>",
	Short: "Mark a task as completed and release GPU resources",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		t := globalStore.GetTask(args[0])
		if t == nil {
			fmt.Printf("Task %s not found\n", args[0])
			return
		}
		globalLifecycle.CompleteTask(args[0])
		saveState()
		fmt.Printf("Task %s marked as completed, GPU resources released\n", args[0])
	},
}

var taskFailCmd = &cobra.Command{
	Use:   "fail <task-id>",
	Short: "Mark a task as failed and release GPU resources",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		t := globalStore.GetTask(args[0])
		if t == nil {
			fmt.Printf("Task %s not found\n", args[0])
			return
		}
		globalLifecycle.FailTask(args[0])
		saveState()
		fmt.Printf("Task %s marked as failed, GPU resources released\n", args[0])
	},
}

var taskCancelCmd = &cobra.Command{
	Use:   "cancel <task-id>",
	Short: "Cancel a queued or running task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		t := globalStore.GetTask(args[0])
		if t == nil {
			fmt.Printf("Task %s not found\n", args[0])
			return
		}
		if t.Status == model.TaskStatusRunning {
			globalStore.ReleaseTaskGPUs(args[0])
		}
		globalQueue.Remove(args[0])
		globalStore.UpdateTaskStatus(args[0], model.TaskStatusFailed)
		saveState()
		fmt.Printf("Task %s cancelled\n", args[0])
	},
}

var taskSimulateCmd = &cobra.Command{
	Use:   "simulate <task-id>",
	Short: "Simulate a running task (mark as running for demo)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		t := globalStore.GetTask(args[0])
		if t == nil {
			fmt.Printf("Task %s not found\n", args[0])
			return
		}
		if t.Status == model.TaskStatusRunning {
			fmt.Printf("Task %s is already running\n", args[0])
			return
		}
		initSchedulerBackground()
		time.Sleep(3 * time.Second)
		saveState()
		t = globalStore.GetTask(args[0])
		fmt.Printf("Task %s current status: %s\n", args[0], t.Status)
		if t.Status == model.TaskStatusRunning {
			fmt.Printf("  Allocated GPUs: %v\n", t.AllocatedGPUs)
			if t.CrossNode {
				fmt.Printf("  ⚠ Cross-node communication - performance may degrade\n")
			}
		}
	},
}

func init() {
	taskCmd.AddCommand(taskStatusCmd)
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskCompleteCmd)
	taskCmd.AddCommand(taskFailCmd)
	taskCmd.AddCommand(taskCancelCmd)
	taskCmd.AddCommand(taskSimulateCmd)
	rootCmd.AddCommand(taskCmd)
}

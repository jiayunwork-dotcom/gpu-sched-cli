package cmd

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gpu-sched-cli/internal/dag"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/gpu-sched-cli/internal/tui"
	"github.com/spf13/cobra"
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
		globalStore.UpdateTaskStatus(args[0], model.TaskStatusCancelled)

		graph := globalStore.GetDepGraph()
		skipped := cascadeSkipFromGraph(graph, args[0])
		if len(skipped) > 0 {
			fmt.Printf("  Cascade skipped %d downstream tasks: %v\n", len(skipped), skipped)
		}

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

var taskReprioritizeCmd = &cobra.Command{
	Use:   "reprioritize <task-id> <new-priority>",
	Short: "Change task priority dynamically (1-10)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		taskID := args[0]
		newPriority, err := strconv.Atoi(args[1])
		if err != nil {
			fmt.Printf("Invalid priority: %s\n", args[1])
			return
		}
		if newPriority < 1 || newPriority > 10 {
			fmt.Println("Priority must be between 1 and 10")
			return
		}

		t := globalStore.GetTask(taskID)
		if t == nil {
			fmt.Printf("Task %s not found\n", taskID)
			return
		}
		if t.Status != model.TaskStatusRunning && t.Status != model.TaskStatusQueued && t.Status != model.TaskStatusSubmitted {
			fmt.Printf("Cannot reprioritize task %s: current status is %s (must be running, queued, or submitted)\n", taskID, t.Status)
			return
		}

		initSchedulerBackground()
		oldPriority, ok, err := globalScheduler.ReprioritizeTask(taskID, newPriority)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		if !ok {
			fmt.Printf("Failed to reprioritize task %s\n", taskID)
			return
		}
		saveState()
		fmt.Printf("Task %s priority changed: %d → %d\n", taskID, oldPriority, newPriority)

		t = globalStore.GetTask(taskID)
		if t.Status == model.TaskStatusRunning {
			fmt.Println("  Status: still running")
			if newPriority < oldPriority {
				fmt.Println("  Triggered reschedule: checking for higher priority queued tasks")
			}
		} else if t.Status == model.TaskStatusQueued || t.Status == model.TaskStatusSubmitted {
			fmt.Printf("  Status: %s\n", t.Status)
			if newPriority >= 8 {
				fmt.Println("  Priority raised to high - preemption may have occurred")
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
	taskCmd.AddCommand(taskReprioritizeCmd)
	rootCmd.AddCommand(taskCmd)
}

func cascadeSkipFromGraph(graph *dag.DependencyGraph, failedTaskID string) []string {
	audit := globalStore.GetAuditLogger()
	return dag.CascadeSkip(graph, failedTaskID,
		func(taskID string) *model.Task { return globalStore.GetTask(taskID) },
		func(taskID string, status model.TaskStatus) { globalStore.UpdateTaskStatus(taskID, status) },
		audit.Record,
	)
}

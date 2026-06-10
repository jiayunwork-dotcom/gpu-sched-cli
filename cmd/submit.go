package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/gpu-sched-cli/internal/model"
)

var submitCmd = &cobra.Command{
	Use:   "submit <task.yaml>",
	Short: "Submit a task from YAML file",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		task, err := loadTaskSpec(args[0])
		if err != nil {
			fmt.Printf("Error loading task: %v\n", err)
			return
		}

		var submitted *model.Task
		if len(task.DependsOn) > 0 {
			submitted, err = globalStore.AddTaskWithDeps(task)
			if err != nil {
				fmt.Printf("Error submitting task: %v\n", err)
				return
			}
			if submitted.Status == model.TaskStatusBlocked {
				globalStore.GetAuditLogger().Record(model.AuditDecisionBlocked, submitted.ID, nil,
					fmt.Sprintf("Task %s blocked: waiting for dependencies", submitted.ID),
					map[string]string{
						"dependencies": fmt.Sprintf("%v", submitted.Spec.DependsOn),
					})
			}
		} else {
			submitted = globalStore.AddTask(task)
		}

		if submitted.Status != model.TaskStatusBlocked {
			globalStore.UpdateTaskStatus(submitted.ID, model.TaskStatusQueued)
			globalQueue.Enqueue(submitted)
		}

		initSchedulerBackground()
		time.Sleep(500 * time.Millisecond)
		saveState()

		fmt.Printf("Task submitted successfully!\n")
		fmt.Printf("  ID: %s\n", submitted.ID)
		fmt.Printf("  Name: %s\n", submitted.Spec.Name)
		fmt.Printf("  Priority: %d (%s)\n", submitted.Spec.Priority, submitted.QueueName())

		if len(submitted.Spec.DependsOn) > 0 {
			fmt.Printf("  Depends on: %v\n", submitted.Spec.DependsOn)
		}

		if submitted.Status == model.TaskStatusBlocked {
			fmt.Printf("  Status: BLOCKED (waiting for dependencies to complete)\n")
		} else if submitted.Status == model.TaskStatusRunning {
			fmt.Printf("  Status: RUNNING (GPUs allocated immediately)\n")
			fmt.Printf("  Allocated GPUs: %v\n", submitted.AllocatedGPUs)
			if submitted.CrossNode {
				fmt.Printf("  ⚠ Cross-node communication - performance may degrade\n")
			}
		} else {
			waitEstimate := globalScheduler.EstimateWaitTime(submitted)
			fmt.Printf("  Estimated wait time: %v\n", waitEstimate)
			fmt.Printf("  Status: Queued (waiting for GPU resources)\n")
		}
	},
}

var submitBatchCmd = &cobra.Command{
	Use:   "submit-batch <dir>",
	Short: "Submit all YAML task files in a directory",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		dir := args[0]
		entries, err := os.ReadDir(dir)
		if err != nil {
			fmt.Printf("Error reading directory: %v\n", err)
			return
		}

		count := 0
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			ext := filepath.Ext(entry.Name())
			if ext != ".yaml" && ext != ".yml" {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			task, err := loadTaskSpec(path)
			if err != nil {
				fmt.Printf("  Skipping %s: %v\n", entry.Name(), err)
				continue
			}
			submitted := globalStore.AddTask(task)
			globalStore.UpdateTaskStatus(submitted.ID, model.TaskStatusQueued)
			globalQueue.Enqueue(submitted)
			fmt.Printf("  Submitted: %s -> %s\n", entry.Name(), submitted.ID)
			count++
		}

		initSchedulerBackground()
		time.Sleep(500 * time.Millisecond)
		saveState()

		fmt.Printf("\nBatch submit complete: %d tasks submitted\n", count)
	},
}

func loadTaskSpec(path string) (*model.TaskSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec model.TaskSpec
	if err := yamlUnmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	if spec.Name == "" {
		return nil, fmt.Errorf("task name is required")
	}
	if spec.GPUReq.MinCount <= 0 {
		return nil, fmt.Errorf("gpu min_count must be > 0")
	}
	if spec.GPUReq.MaxCount < spec.GPUReq.MinCount {
		spec.GPUReq.MaxCount = spec.GPUReq.MinCount
	}
	if spec.Priority < 1 {
		spec.Priority = 1
	}
	if spec.Priority > 10 {
		spec.Priority = 10
	}
	if spec.User == "" {
		spec.User = "default"
	}
	return &spec, nil
}

func init() {
	rootCmd.AddCommand(submitCmd)
	rootCmd.AddCommand(submitBatchCmd)
}

package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/gpu-sched-cli/internal/dag"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/spf13/cobra"
)

type DAGTemplate struct {
	Tasks []model.TaskSpec `yaml:"tasks"`
}

var submitDagCmd = &cobra.Command{
	Use:   "submit-dag <dag.yaml>",
	Short: "Submit a DAG of tasks from a YAML file (atomic batch)",
	Long: `Submit an entire DAG described in a single YAML file. All tasks are validated
before any are registered. If any task fails validation, the entire batch is rolled back.
Task dependencies reference other tasks by name within the same file.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		tmpl, err := loadDAGTemplate(args[0])
		if err != nil {
			fmt.Printf("Error loading DAG template: %v\n", err)
			return
		}

		if len(tmpl.Tasks) == 0 {
			fmt.Println("No tasks found in DAG template")
			return
		}

		for i := range tmpl.Tasks {
			spec := &tmpl.Tasks[i]
			if err := validateTaskSpec(spec); err != nil {
				fmt.Printf("Validation failed for task %q: %v\n", spec.Name, err)
				fmt.Println("Batch submission aborted (no tasks registered)")
				return
			}
		}

		nameSet := make(map[string]bool)
		for _, spec := range tmpl.Tasks {
			if nameSet[spec.Name] {
				fmt.Printf("Duplicate task name: %q\n", spec.Name)
				fmt.Println("Batch submission aborted (no tasks registered)")
				return
			}
			nameSet[spec.Name] = true
		}

		for _, spec := range tmpl.Tasks {
			for _, dep := range spec.DependsOn {
				if !nameSet[dep] {
					existingTask := globalStore.GetTaskBySpecName(dep)
					if existingTask == nil {
						fmt.Printf("Task %q depends on %q which does not exist\n", spec.Name, dep)
						fmt.Println("Batch submission aborted (no tasks registered)")
						return
					}
				}
			}
		}

		tempGraph := globalStore.GetDepGraph().Copy()
		for _, spec := range tmpl.Tasks {
			for _, dep := range spec.DependsOn {
				tempGraph.AddEdge(spec.Name, dep)
			}
		}
		if cyclePath, hasCycle := dag.DetectCycle(tempGraph); hasCycle {
			fmt.Printf("Dependency cycle detected: %s\n", formatCyclePath(cyclePath))
			fmt.Println("Batch submission aborted (no tasks registered)")
			return
		}

		specs := make([]*model.TaskSpec, len(tmpl.Tasks))
		for i := range tmpl.Tasks {
			specs[i] = &tmpl.Tasks[i]
		}

		tasks, err := globalStore.AddTaskWithDepsBatch(specs)
		if err != nil {
			fmt.Printf("Error submitting DAG: %v\n", err)
			fmt.Println("Batch submission aborted (no tasks registered)")
			return
		}

		for _, task := range tasks {
			if task.Status == model.TaskStatusBlocked {
				globalStore.GetAuditLogger().Record(model.AuditDecisionBlocked, task.ID, nil,
					fmt.Sprintf("Task %s blocked: waiting for dependencies", task.ID),
					map[string]string{
						"dependencies": fmt.Sprintf("%v", task.Spec.DependsOn),
					})
			} else {
				globalStore.UpdateTaskStatus(task.ID, model.TaskStatusQueued)
				globalQueue.Enqueue(task)
			}
		}

		initSchedulerBackground()

		fmt.Printf("DAG submitted successfully! %d tasks registered:\n", len(tasks))
		for _, task := range tasks {
			statusStr := string(task.Status)
			depsStr := ""
			if len(task.Spec.DependsOn) > 0 {
				depsStr = fmt.Sprintf(" (depends on: %v)", task.Spec.DependsOn)
			}
			fmt.Printf("  %s: %s [%s]%s\n", task.ID, task.Spec.Name, statusStr, depsStr)
		}

		time.Sleep(500 * time.Millisecond)
		saveState()
	},
}

func loadDAGTemplate(path string) (*DAGTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tmpl DAGTemplate
	if err := yamlUnmarshal(data, &tmpl); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	return &tmpl, nil
}

func validateTaskSpec(spec *model.TaskSpec) error {
	if spec.Name == "" {
		return fmt.Errorf("task name is required")
	}
	if spec.GPUReq.MinCount <= 0 {
		return fmt.Errorf("gpu min_count must be > 0")
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
	return nil
}

func formatCyclePath(path []string) string {
	result := ""
	for i, p := range path {
		if i > 0 {
			result += " → "
		}
		result += p
	}
	return result
}

func init() {
	rootCmd.AddCommand(submitDagCmd)
}

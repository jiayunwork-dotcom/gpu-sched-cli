package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gpu-sched-cli/internal/dag"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/spf13/cobra"
)

var (
	dagRootTask string
	dagDot       bool
	addDepCondition string
	addDepWeight    int
	addDepTimeout   int
)

var dagCmd = &cobra.Command{
	Use:   "dag",
	Short: "Show task dependency graph (DAG)",
	Long:  "Display the current dependency graph as an ASCII tree or Graphviz DOT format.",
	Run: func(cmd *cobra.Command, args []string) {
		graph := globalStore.GetDepGraph()

		if len(graph.AllNodes()) == 0 {
			fmt.Println("No task dependencies found.")
			return
		}

		getTask := func(id string) *model.Task {
			return globalStore.GetTask(id)
		}

		if dagDot {
			fmt.Print(dag.RenderDOT(graph, getTask))
			return
		}

		if dagRootTask != "" {
			t := globalStore.GetTask(dagRootTask)
			if t == nil {
				fmt.Printf("Task %s not found\n", dagRootTask)
				return
			}
			fmt.Print(dag.RenderSubTree(graph, getTask, dagRootTask))
			return
		}

		fmt.Print(dag.RenderASCIITree(graph, getTask, ""))
	},
}

var dagAddDepCmd = &cobra.Command{
	Use:   "add-dep <task-id> <dependency-task-id",
	Short: "Add a dependency to a task",
	Long: `Add a dependency edge from task to another task. Only works for blocked/queued/submitted tasks.
Checks for cycles and rejects if adding would create one.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		taskID := args[0]
		depID := args[1]

		task := globalStore.GetTask(taskID)
		if task == nil {
			fmt.Printf("Error: task %s not found\n", taskID)
			return
		}

		depTask := globalStore.GetTask(depID)
		if depTask == nil {
			fmt.Printf("Error: dependency task %s not found\n", depID)
			return
		}

		condition := model.ParseDepCondition(addDepCondition)
		if addDepWeight <= 0 {
			addDepWeight = 1
		}

		err := globalStore.AddDependency(taskID, depID, condition, addDepWeight, addDepTimeout)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}

		fmt.Printf("Dependency added: %s depends on %s\n", taskID, depID)
		fmt.Printf("  Condition: %s\n", condition)
		fmt.Printf("  Weight: %d\n", addDepWeight)
		if addDepTimeout > 0 {
			fmt.Printf("  Timeout: %d minutes\n", addDepTimeout)
		}

		saveState()
	},
}

var dagRemoveDepCmd = &cobra.Command{
	Use:   "remove-dep <task-id> <dependency-task-id>",
	Short: "Remove a dependency from a task",
	Long: `Remove a dependency edge from a task. Only works for blocked/queued/submitted tasks.
If removing makes all dependencies satisfied, the task becomes queued immediately.`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		taskID := args[0]
		depID := args[1]

		task := globalStore.GetTask(taskID)
		if task == nil {
			fmt.Printf("Error: task %s not found\n", taskID)
			return
		}

		removed, err := globalStore.RemoveDependency(taskID, depID)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		if !removed {
			fmt.Printf("Dependency not found: %s does not depend on %s\n", taskID, depID)
			return
		}

		fmt.Printf("Dependency removed: %s no longer depends on %s\n", taskID, depID)

		task = globalStore.GetTask(taskID)
		if task != nil && task.Status == model.TaskStatusQueued {
			fmt.Printf("Task %s is now queued\n", taskID)
		}

		saveState()
	},
}

var dagExportCmd = &cobra.Command{
	Use:   "export <output.yaml>",
	Short: "Export current DAG to YAML file",
	Long: `Export the current dependency graph to a YAML file.
The format is compatible with submit-dag and can be re-submitted directly.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		outputPath := args[0]

		graph := globalStore.GetDepGraph()
		tasks := globalStore.GetAllTasks()

		tmpl := buildDAGTemplateFromGraph(graph, tasks)

		data, err := yamlMarshal(tmpl)
		if err != nil {
			fmt.Printf("Error marshaling YAML: %v\n", err)
			return
		}

		err = os.WriteFile(outputPath, data, 0644)
		if err != nil {
			fmt.Printf("Error writing file: %v\n", err)
			return
		}

		audit := globalStore.GetAuditLogger()
		audit.Record(model.AuditDecisionDAGExport, "", nil,
			fmt.Sprintf("导出DAG到文件: %s", outputPath), nil)

		fmt.Printf("DAG exported to %s\n", outputPath)
		fmt.Printf("  Tasks: %d\n", len(tmpl.Tasks))
	},
}

var dagImportCmd = &cobra.Command{
	Use:   "import <input.yaml>",
	Short: "Import DAG from YAML file, replacing current dependencies",
	Long: `Import a DAG from a YAML file, replacing the current dependency graph.
Only allowed when no tasks are running.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		inputPath := args[0]

		if globalStore.HasRunningTasks() {
			fmt.Println("Error: cannot import DAG while tasks are running")
			return
		}

		tmpl, err := loadDAGTemplate(inputPath)
		if err != nil {
			fmt.Printf("Error loading DAG template: %v\n", err)
			return
		}

		newGraph, err := buildGraphFromTemplate(tmpl)
		if err != nil {
			fmt.Printf("Error building graph: %v\n", err)
			return
		}

		err = globalStore.ReplaceDepGraph(newGraph)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}

		audit := globalStore.GetAuditLogger()
		audit.Record(model.AuditDecisionDAGImport, "", nil,
			fmt.Sprintf("从文件导入DAG: %s", inputPath), nil)

		fmt.Printf("DAG imported from %s\n", inputPath)
		saveState()
	},
}

var dagSnapshotCmd = &cobra.Command{
	Use:   "snapshot [snapshot-dir]",
	Short: "Save a timestamped snapshot of the current DAG state",
	Long: `Save a snapshot of the current DAG state (dependencies and task states)
as a timestamped file. Default snapshot directory is ./dag_snapshots.`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		snapshotDir := "./dag_snapshots"
		if len(args) > 0 {
			snapshotDir = args[0]
		}

		err := os.MkdirAll(snapshotDir, 0755)
		if err != nil {
			fmt.Printf("Error creating snapshot directory: %v\n", err)
			return
		}

		timestamp := time.Now().Format("20060102-150405")
		filename := fmt.Sprintf("dag-snapshot-%s.yaml", timestamp)
		snapPath := filepath.Join(snapshotDir, filename)

		graph := globalStore.GetDepGraph()
		tasks := globalStore.GetAllTasks()

		snapshot := buildDAGTemplateFromGraph(graph, tasks)

		data, err := yamlMarshal(snapshot)
		if err != nil {
			fmt.Printf("Error marshaling YAML: %v\n", err)
			return
		}

		err = os.WriteFile(snapPath, data, 0644)
		if err != nil {
			fmt.Printf("Error writing snapshot: %v\n", err)
			return
		}

		audit := globalStore.GetAuditLogger()
		audit.Record(model.AuditDecisionDAGSnapshot, "", nil,
			fmt.Sprintf("创建DAG快照: %s", snapPath), nil)

		fmt.Printf("DAG snapshot saved to %s\n", snapPath)
	},
}

var dagRestoreCmd = &cobra.Command{
	Use:   "restore <snapshot-file>",
	Short: "Restore DAG from a snapshot file",
	Long: `Restore DAG state from a previously saved snapshot file.
Only allowed when no tasks are running.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		snapshotPath := args[0]

		if globalStore.HasRunningTasks() {
			fmt.Println("Error: cannot restore DAG while tasks are running")
			return
		}

		tmpl, err := loadDAGTemplate(snapshotPath)
		if err != nil {
			fmt.Printf("Error loading snapshot: %v\n", err)
			return
		}

		newGraph, err := buildGraphFromTemplate(tmpl)
		if err != nil {
			fmt.Printf("Error building graph from snapshot: %v\n", err)
			return
		}

		err = globalStore.ReplaceDepGraph(newGraph)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}

		audit := globalStore.GetAuditLogger()
		audit.Record(model.AuditDecisionDAGRestore, "", nil,
			fmt.Sprintf("从快照恢复DAG: %s", snapshotPath), nil)

		fmt.Printf("DAG restored from %s\n", snapshotPath)
		saveState()
	},
}

func buildDAGTemplateFromGraph(graph *dag.DependencyGraph, tasks []*model.Task) *DAGTemplate {
	taskMap := make(map[string]*model.Task)
	for _, t := range tasks {
		taskMap[t.ID] = t
	}

	allTaskIDs := make(map[string]bool)
	for from, edges := range graph.AllNodes() {
		allTaskIDs[from] = true
		for _, e := range edges {
			allTaskIDs[e.To] = true
		}
	}

	for _, t := range tasks {
		allTaskIDs[t.ID] = true
	}

	var taskSpecs []model.TaskSpec
	for taskID := range allTaskIDs {
		t := taskMap[taskID]
		if t == nil {
			continue
		}
		spec := t.Spec

		depEdges := graph.DependencyEdges(taskID)
		depSpecs := make([]model.DependencySpec, len(depEdges))
		for i, e := range depEdges {
			depTask := taskMap[e.To]
			depName := e.To
			if depTask != nil {
				depName = depTask.Spec.Name
			}
			depSpecs[i] = model.DependencySpec{
				Task:      depName,
				Condition: model.DepCondition(e.Condition),
				Weight:    e.Weight,
				Timeout:   e.Timeout,
			}
		}
		spec.DependsOn = depSpecs
		taskSpecs = append(taskSpecs, spec)
	}

	return &DAGTemplate{Tasks: taskSpecs}
}

func buildGraphFromTemplate(tmpl *DAGTemplate) (*dag.DependencyGraph, error) {
	nameToID := make(map[string]string)
	for _, spec := range tmpl.Tasks {
		task := globalStore.GetTaskBySpecName(spec.Name)
		if task != nil {
			nameToID[spec.Name] = task.ID
		}
	}

	graph := dag.NewDependencyGraph()

	for _, spec := range tmpl.Tasks {
		fromID := spec.Name
		if id, ok := nameToID[spec.Name]; ok {
			fromID = id
		}

		for _, dep := range spec.DependsOn {
			toID := dep.Task
			if id, ok := nameToID[dep.Task]; ok {
				toID = id
			}

			condition := dag.DepCondition(dep.Condition)
			if condition == "" {
				condition = dag.DepConditionCompleted
			}
			weight := dep.Weight
			if weight <= 0 {
				weight = 1
			}
			graph.AddEdgeWithOptions(fromID, toID, condition, weight, dep.Timeout)
		}
	}

	if cyclePath, hasCycle := dag.DetectCycle(graph); hasCycle {
		return nil, fmt.Errorf("dependency cycle detected: %s", formatCyclePath(cyclePath))
	}

	return graph, nil
}

func init() {
	dagCmd.Flags().StringVarP(&dagRootTask, "root", "r", "", "Show sub-graph for a specific task ID (upstream + downstream)")
	dagCmd.Flags().BoolVarP(&dagDot, "dot", "d", false, "Output in Graphviz DOT format")

	dagAddDepCmd.Flags().StringVar(&addDepCondition, "condition", "completed", "Dependency condition: completed, success_or_skip, any_terminal")
	dagAddDepCmd.Flags().IntVarP(&addDepWeight, "weight", "w", 1, "Dependency weight (positive integer)")
	dagAddDepCmd.Flags().IntVarP(&addDepTimeout, "timeout", "t", 0, "Dependency timeout in minutes (0 means no timeout)")

	dagCmd.AddCommand(dagAddDepCmd)
	dagCmd.AddCommand(dagRemoveDepCmd)
	dagCmd.AddCommand(dagExportCmd)
	dagCmd.AddCommand(dagImportCmd)
	dagCmd.AddCommand(dagSnapshotCmd)
	dagCmd.AddCommand(dagRestoreCmd)

	rootCmd.AddCommand(dagCmd)
}

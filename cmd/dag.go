package cmd

import (
	"fmt"

	"github.com/gpu-sched-cli/internal/dag"
	"github.com/gpu-sched-cli/internal/model"
	"github.com/spf13/cobra"
)

var (
	dagRootTask string
	dagDot      bool
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

func init() {
	dagCmd.Flags().StringVarP(&dagRootTask, "root", "r", "", "Show sub-graph for a specific task ID (upstream + downstream)")
	dagCmd.Flags().BoolVarP(&dagDot, "dot", "d", false, "Output in Graphviz DOT format")
	rootCmd.AddCommand(dagCmd)
}

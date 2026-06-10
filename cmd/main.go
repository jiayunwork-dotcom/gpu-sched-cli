package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func yamlUnmarshal(data []byte, v interface{}) error {
	return yaml.Unmarshal(data, v)
}

var (
	stateFile string
)

var rootCmd = &cobra.Command{
	Use:   "gpu-sched",
	Short: "GPU Cluster Resource Scheduler CLI",
	Long:  "A CLI tool for scheduling AI training and inference workloads across heterogeneous GPU clusters.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return initGlobals()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&clusterFile, "cluster", "c", "cluster.yaml", "Cluster configuration YAML file")
	rootCmd.PersistentFlags().StringVarP(&schedulerFile, "scheduler", "s", "scheduler.yaml", "Scheduler configuration YAML file")
	rootCmd.PersistentFlags().StringVar(&stateFile, "state", "", "State file path (default: ~/.gpu-sched-state.json)")
}

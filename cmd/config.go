package cmd

import (
	"fmt"
	"os"

	"github.com/gpu-sched-cli/internal/model"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management commands",
}

var configReloadCmd = &cobra.Command{
	Use:    "reload",
	Short:  "Reload scheduler configuration from file",
	Aliases: []string{"r"},
	Run: func(cmd *cobra.Command, args []string) {
		cfg, err := loadSchedulerConfig(schedulerFile)
		if err != nil {
			fmt.Printf("Error loading config: %v\n", err)
			return
		}
		globalStore.SetConfig(cfg)
		saveState()
		fmt.Println("Configuration reloaded successfully (running tasks unaffected)")
	},
}

var configShowCmd = &cobra.Command{
	Use:    "show",
	Short:  "Show current scheduler configuration",
	Aliases: []string{"s"},
	Run: func(cmd *cobra.Command, args []string) {
		cfg := globalStore.GetConfig()
		fmt.Printf("Scheduler Configuration:\n")
		fmt.Printf("  Strategy:           %s\n", cfg.Strategy)
		fmt.Printf("  Preempt Enabled:    %v\n", cfg.PreemptEnabled)
		fmt.Printf("  Sharing Enabled:    %v\n", cfg.SharingEnabled)
		fmt.Printf("  Timeout Enabled:    %v\n", cfg.TimeoutEnabled)
		fmt.Printf("  Timeout Multiplier: %.1f\n", cfg.TimeoutMultiplier)
		fmt.Printf("  Max Retries:        %d\n", cfg.MaxRetries)
		fmt.Printf("  Gang Wait Timeout:  %d min\n", cfg.GangWaitTimeoutMin)
		fmt.Printf("  Queue Weights:\n")
		for name, w := range cfg.QueueWeights {
			fmt.Printf("    %s: %.1f\n", name, w)
		}
		if len(cfg.UserQuotas) > 0 {
			fmt.Printf("  User Quotas (GPU-min):\n")
			for user, q := range cfg.UserQuotas {
				usage := globalStore.GetUserUsage(user)
				fmt.Printf("    %s: %.0f (used: %.0f)\n", user, q, usage)
			}
		}
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value (hot-reload)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := globalStore.GetConfig()
		key, value := args[0], args[1]
		switch key {
		case "strategy":
			cfg.Strategy = model.ScheduleStrategy(value)
		case "preempt_enabled":
			cfg.PreemptEnabled = value == "true"
		case "sharing_enabled":
			cfg.SharingEnabled = value == "true"
		case "timeout_enabled":
			cfg.TimeoutEnabled = value == "true"
		case "timeout_multiplier":
			var f float64
			fmt.Sscanf(value, "%f", &f)
			cfg.TimeoutMultiplier = f
		case "max_retries":
			var i int
			fmt.Sscanf(value, "%d", &i)
			cfg.MaxRetries = i
		case "gang_wait_timeout_min":
			var i int
			fmt.Sscanf(value, "%d", &i)
			cfg.GangWaitTimeoutMin = i
		default:
			fmt.Printf("Unknown config key: %s\n", key)
			os.Exit(1)
		}
		globalStore.SetConfig(cfg)
		saveState()
		fmt.Printf("Config updated: %s = %s (effective immediately)\n", key, value)
	},
}

func init() {
	configCmd.AddCommand(configReloadCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}

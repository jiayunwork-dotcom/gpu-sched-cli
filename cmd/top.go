package cmd

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"github.com/gpu-sched-cli/internal/tui"
)

var topCmd = &cobra.Command{
	Use:   "top",
	Short: "Real-time cluster monitoring dashboard",
	Run: func(cmd *cobra.Command, args []string) {
		model := tui.NewTopModel(globalStore, globalQueue)
		p := tea.NewProgram(model, tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fmt.Printf("Error running TUI: %v\n", err)
		}
	},
}

func init() {
	rootCmd.AddCommand(topCmd)
}

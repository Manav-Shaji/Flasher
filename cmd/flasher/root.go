package main

import (
	"context"
	"flasher/internal/config"
	"flasher/internal/core"
	"flasher/internal/tui"
	"flasher/internal/version"
	"fmt"
	"os"
	"runtime"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

var (
	applicationContainer *config.App
)

var rootCmd = &cobra.Command{
	Use:   "flasher",
	Short: "Flasher - Android ROM Flashing TUI/CLI",
	Long:  `Flasher is a powerful TUI and CLI application for flashing Android ROMs via Fastboot and ADB.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Name() == "inspect" {
			return nil
		}
		var err error
		applicationContainer, err = config.Initialize()
		if err != nil {
			return err
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		m := tui.NewModel(applicationContainer)
		p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
		if _, err := p.Run(); err != nil {
			return fmt.Errorf("error starting TUI: %w", err)
		}
		return nil
	},
}

func Execute() {
	cobra.MousetrapHelpText = ""

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func setCommandOutputSink(cmd *cobra.Command) {
	var mu sync.Mutex
	applicationContainer.Engine.SetSink(func(level core.LogLevel, line string) {
		mu.Lock()
		defer mu.Unlock()
		if level == core.LogError {
			fmt.Fprintln(cmd.ErrOrStderr(), line)
			return
		}
		fmt.Fprintln(cmd.OutOrStdout(), line)
	})
}

func main() {
	// Execute the root Cobra command
	Execute()
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of Flasher",
	Long:  `All software has versions. This is Flasher's.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", version.AppName, version.Version)
		fmt.Fprintf(cmd.OutOrStdout(), "Commit: %s\n", version.Commit)
		fmt.Fprintf(cmd.OutOrStdout(), "Build Date: %s\n", version.BuildDate)
		fmt.Fprintf(cmd.OutOrStdout(), "Go Version: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

var devicesCmd = &cobra.Command{
	Use:   "devices",
	Short: "List connected ADB and Fastboot devices",
	Long:  `Queries and prints currently connected devices using platform tools.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		setCommandOutputSink(cmd)
		ctx := context.Background()

		fmt.Fprintln(cmd.OutOrStdout(), "Querying Fastboot devices...")
		if err := applicationContainer.Engine.RunCommand(ctx, "fastboot", "devices"); err != nil {
			applicationContainer.Logger.Warn("Fastboot devices check failed")
		}

		fmt.Fprintln(cmd.OutOrStdout(), "\nQuerying ADB devices...")
		if err := applicationContainer.Engine.RunCommand(ctx, "adb", "devices"); err != nil {
			applicationContainer.Logger.Warn("ADB devices check failed")
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(devicesCmd)
}

package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"flasher/internal/core"
	"github.com/spf13/cobra"
)

var inspectSHA256 string

var inspectCmd = &cobra.Command{
	Use:   "inspect [package]",
	Short: "Inspect a ROM archive without executing its installer",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := filepath.Abs(args[0])
		if err != nil {
			return fmt.Errorf("resolve ROM package path: %w", err)
		}
		inspection, err := core.InspectROM(path, inspectSHA256)
		if err != nil {
			return fmt.Errorf("inspect ROM package: %w", err)
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(inspection); err != nil {
			return fmt.Errorf("write ROM inspection report: %w", err)
		}
		return nil
	},
}

func init() {
	inspectCmd.Flags().StringVar(&inspectSHA256, "sha256", "",
		"Compare against an expected SHA-256 obtained from an independent trusted source")
	rootCmd.AddCommand(inspectCmd)
}

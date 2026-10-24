package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"flasher/internal/core"
	"flasher/internal/platform"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

var (
	forceFlash       bool
	flashSHA256      string
	dryRunFlash      bool
	acknowledgeFlash bool
)

var flashCmd = &cobra.Command{
	Use:   "flash [partition] [image]",
	Short: "Flash an image to a specific partition",
	Long: `Flash an Android partition using fastboot.

Use --dry-run to print the target, operation, checksum and compatibility
results, and every blocker without issuing a device-changing command.
An actual flash requires --yes for explicit risk acknowledgement. --force
only overrides low or unknown battery readings; it cannot bypass other checks.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		setCommandOutputSink(cmd)
		partition := args[0]
		path := args[1]

		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}

		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			return fmt.Errorf("image file not found: %s", absPath)
		}
		inputFingerprint, err := core.CalculateInputFingerprint(absPath)
		if err != nil {
			return fmt.Errorf("calculate input content fingerprint: %w", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		deviceState, err := scanDeviceStateWithRetry(platform.CheckDeviceStateFresh, 3, 300*time.Millisecond)
		if err != nil {
			return err
		}
		options := core.FlashOptions{
			SkipBattery: forceFlash, RiskAcknowledged: acknowledgeFlash || dryRunFlash,
			ExpectedSHA256: flashSHA256, InputFingerprintSHA256: inputFingerprint,
		}
		plan := applicationContainer.Engine.FlashService.PlanFlash(ctx, deviceState, partition, absPath, options)
		err = runFlashPlan(cmd, plan, dryRunFlash, func() error {
			if forceFlash {
				warning := "WARN: --force bypasses low/unknown battery checks; invalid battery readings still block. Power loss mid-flash can brick the device."
				fmt.Fprintln(cmd.ErrOrStderr(), warning)
				applicationContainer.Engine.Emit(core.LogInfo, warning)
				applicationContainer.Logger.Warn("Battery check skipped with --force", zap.String("warning", warning))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Flashing %s to %s...\n", absPath, partition)
			if err := applicationContainer.Engine.FlashService.FlashImageWithPolicy(ctx, deviceState, partition, absPath, options); err != nil {
				return fmt.Errorf("flash failed: %w", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if dryRunFlash {
			return nil
		}

		fmt.Fprintln(cmd.OutOrStdout(), "Flash complete!")
		return nil
	},
}

func runFlashPlan(cmd *cobra.Command, plan core.FlashPlan, dryRun bool, execute func() error) error {
	if dryRun {
		printFlashPlan(cmd, plan)
		if err := plan.Error(); err != nil {
			return fmt.Errorf("dry-run blocked: %w", err)
		}
		return nil
	}
	if err := plan.Error(); err != nil {
		return fmt.Errorf("flash validation failed: %w", err)
	}
	if execute == nil {
		return fmt.Errorf("flash execution callback is unavailable")
	}
	return execute()
}

func printFlashPlan(cmd *cobra.Command, plan core.FlashPlan) {
	fmt.Fprintln(cmd.OutOrStdout(), "DRY RUN - no device-changing command will be sent.")
	fmt.Fprintf(cmd.OutOrStdout(), "Target: %s device %q\n", plan.TargetMode, plan.TargetSerial)
	if plan.TargetManufacturer != "" || plan.TargetModel != "" || plan.TargetCodename != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Device: %s %s (%s)\n",
			plan.TargetManufacturer, plan.TargetModel, plan.TargetCodename)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Operation: fastboot -s %q flash %s \"%s\"\n",
		plan.TargetSerial, plan.Partition, plan.InputFile)
	fmt.Fprintf(cmd.OutOrStdout(), "Partition: %s\n", plan.Partition)
	fmt.Fprintf(cmd.OutOrStdout(), "Input file: %s\n", plan.InputFile)
	fmt.Fprintf(cmd.OutOrStdout(), "Content identity (calculated SHA-256): %s\n", plan.InputFingerprintSHA256)
	fmt.Fprintf(cmd.OutOrStdout(), "Checksum: %s\n", plan.ChecksumStatus)
	fmt.Fprintf(cmd.OutOrStdout(), "Compatibility: %s\n", plan.CompatibilityStatus)
	for _, warning := range plan.Warnings {
		fmt.Fprintf(cmd.OutOrStdout(), "Warning: %s\n", warning)
	}
	if len(plan.BlockingReasons) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Blocking reasons: none")
		return
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Blocking reasons:")
	for _, reason := range plan.BlockingReasons {
		fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", reason)
	}
}

func scanDeviceStateWithRetry(scan func() tea.Msg, retries int, delay time.Duration) (platform.DeviceState, error) {
	for attempt := 0; ; attempt++ {
		result := scan()
		switch msg := result.(type) {
		case platform.DeviceUpdateMsg:
			return platform.DeviceState(msg), nil
		case platform.SkipUpdateMsg:
			if attempt >= retries {
				return platform.DeviceState{}, fmt.Errorf("device scan is already in progress; try again")
			}
			time.Sleep(delay)
		default:
			return platform.DeviceState{}, fmt.Errorf("unexpected device scan result: %T", result)
		}
	}
}

func init() {
	flashCmd.Flags().BoolVar(&forceFlash, "force", false, "Bypass low/unknown battery checks (invalid readings still block)")
	flashCmd.Flags().StringVar(&flashSHA256, "sha256", "", "Verify the image against this SHA-256 checksum")
	flashCmd.Flags().BoolVar(&dryRunFlash, "dry-run", false, "Show the flash plan and blockers without sending a device-changing command")
	flashCmd.Flags().BoolVar(&acknowledgeFlash, "yes", false, "Acknowledge the risk and authorize execution (ignored for device writes during --dry-run)")
	rootCmd.AddCommand(flashCmd)
}

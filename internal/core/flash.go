package core

import (
	"context"
	"errors"
	"flasher/internal/platform"
	"fmt"
	"os"
	"strings"
)

// FlashService defines operations for flashing devices.
type FlashService interface {
	ValidateDeviceForFlash(ctx context.Context, state platform.DeviceState, imgPath string) error
	ValidateDeviceForFlashWithOptions(ctx context.Context, state platform.DeviceState, imgPath string, skipBattery bool) error
	ValidateDeviceForFlashAllowUnknownBattery(ctx context.Context, state platform.DeviceState, imgPath string) error
	ValidateDeviceForWipeSuper(ctx context.Context, state platform.DeviceState, imgPath string) error
	ValidateDeviceForSideload(ctx context.Context, state platform.DeviceState, imgPath string) error
	ValidateDeviceForSideloadWithOptions(ctx context.Context, state platform.DeviceState, imgPath string, allowUnknownBattery bool) error
	PlanFlash(ctx context.Context, state platform.DeviceState, partition, path string, options FlashOptions) FlashPlan
	PlanWipeSuper(ctx context.Context, state platform.DeviceState, path string, options FlashOptions) FlashPlan
	PlanSideload(ctx context.Context, state platform.DeviceState, path string, options FlashOptions) FlashPlan
	FlashImage(ctx context.Context, state platform.DeviceState, partition, path string) error
	FlashImageWithOptions(ctx context.Context, state platform.DeviceState, partition, path string, skipBattery, allowUnknownBattery bool) error
	FlashImageWithPolicy(ctx context.Context, state platform.DeviceState, partition, path string, options FlashOptions) error
	WipeSuper(ctx context.Context, state platform.DeviceState, path string) error
	WipeSuperWithPolicy(ctx context.Context, state platform.DeviceState, path string, options FlashOptions) error
	Sideload(ctx context.Context, state platform.DeviceState, path string) error
	SideloadWithPolicy(ctx context.Context, state platform.DeviceState, path string, options FlashOptions) error
}

const (
	BatteryMinimumPercent = 30
	BatteryMinimumMV      = 3500
)

var checkDeviceTarget = platform.ValidateDeviceTarget

func CheckBattery(state platform.DeviceState) error {
	if state.BatteryInvalid {
		return ErrBatteryInvalid
	}
	switch {
	case state.BatteryPct > 0 || (state.BatteryPct == 0 && strings.Contains(state.Battery, "%")):
		if state.BatteryPct > 100 {
			return ErrBatteryInvalid
		}
		if state.BatteryPct < BatteryMinimumPercent {
			return fmt.Errorf("%w: %d%% is below %d%%", ErrBatteryLow, state.BatteryPct, BatteryMinimumPercent)
		}
		return nil
	case state.BatteryMV > 0:
		if state.BatteryMV < 1000 || state.BatteryMV > 6000 {
			return ErrBatteryInvalid
		}
		if state.BatteryMV < BatteryMinimumMV {
			return fmt.Errorf("%w: %d mV is below %d mV", ErrBatteryLow, state.BatteryMV, BatteryMinimumMV)
		}
		return nil
	default:
		return ErrBatteryUnknown
	}
}

type defaultFlashService struct {
	exec          Executor
	compatibility CompatibilityEvaluator
}

// NewFlashService creates a new FlashService using the provided executor.
func NewFlashService(exec Executor) FlashService {
	return NewFlashServiceWithCompatibilityEvaluator(exec, CompatibilityEvaluatorFunc(EvaluateROMCompatibility))
}

func NewFlashServiceWithCompatibilityEvaluator(exec Executor, evaluator CompatibilityEvaluator) FlashService {
	if evaluator == nil {
		evaluator = CompatibilityEvaluatorFunc(EvaluateROMCompatibility)
	}
	return &defaultFlashService{exec: exec, compatibility: evaluator}
}

func (s *defaultFlashService) ValidateDeviceForFlash(ctx context.Context, state platform.DeviceState, imgPath string) error {
	return s.validateDeviceForFlash(state, imgPath, false, false, "fastboot-flash")
}

func (s *defaultFlashService) ValidateDeviceForFlashWithOptions(ctx context.Context, state platform.DeviceState, imgPath string, skipBattery bool) error {
	return s.validateDeviceForFlash(state, imgPath, skipBattery, false, "fastboot-flash")
}

func (s *defaultFlashService) ValidateDeviceForFlashAllowUnknownBattery(ctx context.Context, state platform.DeviceState, imgPath string) error {
	return s.validateDeviceForFlash(state, imgPath, false, true, "fastboot-flash")
}

func (s *defaultFlashService) ValidateDeviceForWipeSuper(ctx context.Context, state platform.DeviceState, imgPath string) error {
	return s.validateDeviceForFlash(state, imgPath, false, false, "fastboot-wipe-super")
}

func (s *defaultFlashService) validateDeviceForFlash(state platform.DeviceState, imgPath string, skipBattery, allowUnknownBattery bool, method string) error {
	if state.Mode != platform.ModeFastboot {
		return fmt.Errorf("%w: expected FASTBOOT mode, got %s", ErrDeviceDisconnected, state.Mode)
	}
	if state.Unlocked != platform.Yes {
		status := "unknown"
		if state.Unlocked == platform.No {
			status = "locked"
		}
		return fmt.Errorf("%w: bootloader unlock state must be explicitly verified (status: %s)", ErrBootloaderLocked, status)
	}

	batteryErr := CheckBattery(state)
	if errors.Is(batteryErr, ErrBatteryInvalid) {
		return batteryErr
	}
	if !skipBattery && batteryErr != nil && !(allowUnknownBattery && errors.Is(batteryErr, ErrBatteryUnknown)) {
		return batteryErr
	}

	// Verify image exists
	info, err := os.Stat(imgPath)
	if err != nil {
		return fmt.Errorf("%w: image file cannot be accessed: %v", ErrFlashFailed, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: image path is not a regular file", ErrFlashFailed)
	}
	requiredPartition := ""
	if method == "fastboot-wipe-super" {
		requiredPartition = "super"
	}
	if err := s.requireROMCompatibilityForPartition(state, imgPath, method, requiredPartition); err != nil {
		return err
	}

	return nil
}

func (s *defaultFlashService) ValidateDeviceForSideload(ctx context.Context, state platform.DeviceState, imgPath string) error {
	return s.ValidateDeviceForSideloadWithOptions(ctx, state, imgPath, false)
}

func (s *defaultFlashService) ValidateDeviceForSideloadWithOptions(ctx context.Context, state platform.DeviceState, imgPath string, allowUnknownBattery bool) error {
	return s.ValidateDeviceForSideloadWithPolicy(ctx, state, imgPath, FlashOptions{
		AllowUnknownBattery: allowUnknownBattery,
	})
}

func (s *defaultFlashService) ValidateDeviceForSideloadWithPolicy(ctx context.Context, state platform.DeviceState, imgPath string, options FlashOptions) error {
	if state.Mode != platform.ModeRecovery && state.Mode != platform.ModeSideload && state.Mode != platform.ModeDevice {
		return fmt.Errorf("%w: expected RECOVERY/SIDELOAD mode, got %s", ErrDeviceDisconnected, state.Mode)
	}

	// Verify zip exists
	info, err := os.Stat(imgPath)
	if err != nil {
		return fmt.Errorf("%w: zip file cannot be accessed: %v", ErrFlashFailed, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: zip path is not a regular file", ErrFlashFailed)
	}

	if !strings.HasSuffix(strings.ToLower(imgPath), ".zip") {
		return fmt.Errorf("%w: expected .zip file for sideload", ErrFlashFailed)
	}
	batteryErr := CheckBattery(state)
	if batteryErr != nil && !(options.AllowUnknownBattery && errors.Is(batteryErr, ErrBatteryUnknown)) {
		return batteryErr
	}
	report := s.evaluateCompatibility(state, imgPath, "adb-sideload")
	if report.Allowed && report.State == CompatibilityVerified {
		return nil
	}
	if options.AllowCandidateCompatibility && options.RiskAcknowledged &&
		sideloadCompatibilityCanProceed(report, state) {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrCompatibility, report.Error())
}

func (s *defaultFlashService) requireROMCompatibilityForPartition(state platform.DeviceState, path, method, partition string) error {
	report := s.evaluateCompatibility(state, path, method)
	if report.Allowed && report.State == CompatibilityVerified {
		if partition != "" && !hasIdentifier(report.RequiredPartitions, partition) {
			return fmt.Errorf("%w: requested partition %q is not in the independently verified package partition requirements",
				ErrCompatibility, partition)
		}
		return nil
	}
	return fmt.Errorf("%w: %s", ErrCompatibility, report.Error())
}

func (s *defaultFlashService) FlashImage(ctx context.Context, state platform.DeviceState, partition, path string) error {
	return s.FlashImageWithOptions(ctx, state, partition, path, false, false)
}

func (s *defaultFlashService) FlashImageWithOptions(ctx context.Context, state platform.DeviceState, partition, path string, skipBattery, allowUnknownBattery bool) error {
	return s.FlashImageWithPolicy(ctx, state, partition, path, FlashOptions{
		SkipBattery: skipBattery, AllowUnknownBattery: allowUnknownBattery,
	})
}

func (s *defaultFlashService) FlashImageWithPolicy(ctx context.Context, state platform.DeviceState, partition, path string, options FlashOptions) error {
	initialPlan := s.PlanFlash(ctx, state, partition, path, options)
	if err := initialPlan.Error(); err != nil {
		return fmt.Errorf("flash validation failed: %w", err)
	}
	snapshot, fingerprint, cleanup, err := stageInputSnapshot(ctx, path, options.InputFingerprintSHA256)
	if err != nil {
		return fmt.Errorf("flash validation failed: %w", err)
	}
	defer cleanup()
	options.InputFingerprintSHA256 = fingerprint
	plan := s.PlanFlash(ctx, state, partition, snapshot, options)
	if err := plan.Error(); err != nil {
		return fmt.Errorf("flash validation failed: %w", err)
	}
	args := []string{"-s", state.Serial, "flash", partition, snapshot}
	return runAuthorizedCommand(ctx, s.exec, "fastboot", args...)
}

func validPartitionName(partition string) bool {
	if partition == "" || len(partition) > 128 {
		return false
	}
	for i, char := range partition {
		isLetter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
		isDigit := char >= '0' && char <= '9'
		if (i == 0 && !isLetter) || (i > 0 && !isLetter && !isDigit &&
			char != '_' && char != '-' && char != '.') {
			return false
		}
	}
	return true
}

func (s *defaultFlashService) WipeSuper(ctx context.Context, state platform.DeviceState, path string) error {
	return s.WipeSuperWithPolicy(ctx, state, path, FlashOptions{})
}

func (s *defaultFlashService) WipeSuperWithPolicy(ctx context.Context, state platform.DeviceState, path string, options FlashOptions) error {
	plan := s.PlanWipeSuper(ctx, state, path, options)
	if err := plan.Error(); err != nil {
		return fmt.Errorf("wipe-super validation failed: %w", err)
	}
	snapshot, _, cleanup, err := stageInputSnapshot(ctx, path, options.InputFingerprintSHA256)
	if err != nil {
		return fmt.Errorf("wipe-super validation failed: %w", err)
	}
	defer cleanup()
	if err := s.validateDeviceForFlash(state, snapshot, options.SkipBattery, options.AllowUnknownBattery, "fastboot-wipe-super"); err != nil {
		return err
	}
	if options.ExpectedSHA256 != "" {
		if err := VerifyImageSHA256(snapshot, options.ExpectedSHA256); err != nil {
			return fmt.Errorf("checksum validation failed: %w", err)
		}
	}
	if err := validateSelectedTarget(state); err != nil {
		return err
	}
	args := []string{"-s", state.Serial, "wipe-super", snapshot}
	return runAuthorizedCommand(ctx, s.exec, "fastboot", args...)
}

func (s *defaultFlashService) Sideload(ctx context.Context, state platform.DeviceState, path string) error {
	return s.SideloadWithPolicy(ctx, state, path, FlashOptions{})
}

func (s *defaultFlashService) SideloadWithPolicy(ctx context.Context, state platform.DeviceState, path string, options FlashOptions) error {
	plan := s.PlanSideload(ctx, state, path, options)
	if err := plan.Error(); err != nil {
		return fmt.Errorf("sideload validation failed: %w", err)
	}
	var snapshot, fingerprint string
	var cleanup func()
	var err error
	if options.InputFingerprintSHA256 != "" || options.ExpectedSHA256 != "" {
		expectedFingerprint := options.InputFingerprintSHA256
		if expectedFingerprint == "" {
			expectedFingerprint = options.ExpectedSHA256
		}
		snapshot, fingerprint, cleanup, err = stageInputSnapshot(ctx, path, expectedFingerprint)
	} else {
		snapshot, cleanup, err = stageInputSnapshotWithoutHash(ctx, path)
	}
	if err != nil {
		return fmt.Errorf("sideload validation failed: %w", err)
	}
	defer cleanup()
	if options.ExpectedSHA256 != "" && !strings.EqualFold(fingerprint, strings.TrimSpace(options.ExpectedSHA256)) {
		return fmt.Errorf("checksum validation failed: %w", ErrHashMismatch)
	}
	if err := s.ValidateDeviceForSideloadWithPolicy(ctx, state, snapshot, options); err != nil {
		return err
	}
	if err := validateSelectedTarget(state); err != nil {
		return err
	}
	args := []string{"-s", state.Serial, "sideload", snapshot}
	return runAuthorizedCommand(ctx, s.exec, "adb", args...)
}

func validateSelectedTarget(state platform.DeviceState) error {
	if err := checkDeviceTarget(state.Serial, state.Mode); err != nil {
		return fmt.Errorf("%w: %v", ErrDeviceDisconnected, err)
	}
	return nil
}

type FlashOptions struct {
	SkipBattery                 bool
	AllowUnknownBattery         bool
	AllowCandidateCompatibility bool
	RiskAcknowledged            bool
	ExpectedSHA256              string
	InputFingerprintSHA256      string
}

type FlashPlan struct {
	TargetSerial           string
	TargetMode             platform.DeviceMode
	TargetManufacturer     string
	TargetModel            string
	TargetCodename         string
	Operation              string
	Partition              string
	InputFile              string
	InputFingerprintSHA256 string
	ChecksumStatus         string
	CompatibilityStatus    string
	BlockingReasons        []string
	Warnings               []string
	err                    error
}

func (p FlashPlan) Error() error {
	return p.err
}

type authorizedCommand struct {
	binary string
	args   []string
}

type authorizedCommandContextKey struct{}

func authorizeCommand(ctx context.Context, binary string, args ...string) context.Context {
	authorization := authorizedCommand{binary: binary, args: append([]string(nil), args...)}
	return context.WithValue(ctx, authorizedCommandContextKey{}, authorization)
}

func commandAuthorized(ctx context.Context, binary string, args []string) bool {
	authorization, ok := ctx.Value(authorizedCommandContextKey{}).(authorizedCommand)
	if !ok || authorization.binary != binary || len(authorization.args) != len(args) {
		return false
	}
	for i := range args {
		if authorization.args[i] != args[i] {
			return false
		}
	}
	return true
}

func (s *defaultFlashService) PlanFlash(ctx context.Context, state platform.DeviceState, partition, path string, options FlashOptions) FlashPlan {
	plan := FlashPlan{
		TargetSerial: state.Serial, TargetMode: state.Mode,
		TargetManufacturer: state.Manufacturer, TargetModel: state.Model,
		TargetCodename: state.Codename,
		Operation:      "fastboot flash", Partition: partition, InputFile: path,
		InputFingerprintSHA256: options.InputFingerprintSHA256,
		ChecksumStatus:         "NOT PROVIDED",
		CompatibilityStatus:    string(CompatibilityUnknown),
		BlockingReasons:        []string{}, Warnings: []string{},
	}
	var blockers []error
	addBlocker := func(err error) {
		if err != nil {
			blockers = append(blockers, err)
			plan.BlockingReasons = append(plan.BlockingReasons, err.Error())
		}
	}

	if state.Mode != platform.ModeFastboot {
		addBlocker(fmt.Errorf("%w: expected FASTBOOT mode, got %s", ErrDeviceDisconnected, state.Mode))
	} else {
		if state.Unlocked != platform.Yes {
			status := "unknown"
			if state.Unlocked == platform.No {
				status = "locked"
			}
			addBlocker(fmt.Errorf("%w: bootloader unlock state must be explicitly verified (status: %s)", ErrBootloaderLocked, status))
		}
		if strings.TrimSpace(state.Serial) == "" || state.Serial == "-" {
			addBlocker(fmt.Errorf("%w: selected device has no usable serial number", ErrDeviceDisconnected))
		} else if err := checkDeviceTarget(state.Serial, state.Mode); err != nil {
			addBlocker(fmt.Errorf("%w: %v", ErrDeviceDisconnected, err))
		}
	}
	if !validPartitionName(partition) {
		addBlocker(fmt.Errorf("%w: invalid partition name %q", ErrFlashFailed, partition))
	}
	if !options.RiskAcknowledged {
		addBlocker(ErrRiskAcknowledgement)
	}
	batteryErr := CheckBattery(state)
	if errors.Is(batteryErr, ErrBatteryInvalid) {
		addBlocker(batteryErr)
	} else if !options.SkipBattery && batteryErr != nil &&
		!(options.AllowUnknownBattery && errors.Is(batteryErr, ErrBatteryUnknown)) {
		addBlocker(batteryErr)
	}

	info, statErr := os.Stat(path)
	if statErr != nil {
		addBlocker(fmt.Errorf("%w: image file cannot be accessed: %v", ErrFlashFailed, statErr))
	} else if !info.Mode().IsRegular() {
		addBlocker(fmt.Errorf("%w: image path is not a regular file", ErrFlashFailed))
	} else {
		fingerprint, fingerprintErr := CalculateInputFingerprint(path)
		if fingerprintErr != nil {
			addBlocker(fmt.Errorf("%w: input fingerprint failed: %v", ErrFlashFailed, fingerprintErr))
		} else {
			plan.InputFingerprintSHA256 = fingerprint
			if options.InputFingerprintSHA256 != "" &&
				!strings.EqualFold(fingerprint, strings.TrimSpace(options.InputFingerprintSHA256)) {
				addBlocker(fmt.Errorf("%w: selected %s, current %s", ErrInputChanged,
					options.InputFingerprintSHA256, fingerprint))
			}
		}
		if options.ExpectedSHA256 == "" {
			plan.Warnings = append(plan.Warnings, "SHA-256 expected checksum was not supplied; calculated hash is not trusted.")
		} else {
			if err := VerifyImageSHA256(path, options.ExpectedSHA256); err != nil {
				plan.ChecksumStatus = "EXPECTED_MISMATCH"
				addBlocker(fmt.Errorf("checksum validation failed: %w", err))
			} else {
				plan.ChecksumStatus = "EXPECTED_MATCH"
			}
		}
		report := s.evaluateCompatibility(state, path, "fastboot-flash")
		plan.CompatibilityStatus = string(report.State)
		if !report.Allowed || report.State != CompatibilityVerified {
			addBlocker(fmt.Errorf("%w: %s", ErrCompatibility, report.Error()))
		} else if !hasIdentifier(report.RequiredPartitions, partition) {
			addBlocker(fmt.Errorf("%w: requested partition %q is not in the independently verified package partition requirements",
				ErrCompatibility, partition))
		}
	}

	if err := ctx.Err(); err != nil {
		addBlocker(fmt.Errorf("flash plan canceled: %w", err))
	}
	plan.err = errors.Join(blockers...)
	return plan
}

func (s *defaultFlashService) PlanWipeSuper(ctx context.Context, state platform.DeviceState, path string, options FlashOptions) FlashPlan {
	return s.planImageOperation(ctx, state, "fastboot wipe-super", path, "fastboot-wipe-super", "super", options)
}

func (s *defaultFlashService) PlanSideload(ctx context.Context, state platform.DeviceState, path string, options FlashOptions) FlashPlan {
	return s.planImageOperation(ctx, state, "adb sideload", path, "adb-sideload", "", options)
}

func (s *defaultFlashService) planImageOperation(
	ctx context.Context,
	state platform.DeviceState,
	operation, path, method, requiredPartition string,
	options FlashOptions,
) FlashPlan {
	plan := FlashPlan{
		TargetSerial: state.Serial, TargetMode: state.Mode,
		TargetManufacturer: state.Manufacturer, TargetModel: state.Model,
		TargetCodename: state.Codename, Operation: operation, InputFile: path,
		InputFingerprintSHA256: options.InputFingerprintSHA256,
		ChecksumStatus:         "NOT PROVIDED", CompatibilityStatus: string(CompatibilityUnknown),
		BlockingReasons: []string{}, Warnings: []string{},
	}
	var blockers []error
	addBlocker := func(err error) {
		if err != nil {
			blockers = append(blockers, err)
			plan.BlockingReasons = append(plan.BlockingReasons, err.Error())
		}
	}
	if !options.RiskAcknowledged {
		addBlocker(ErrRiskAcknowledgement)
	}
	if requiredPartition == "super" {
		addBlocker(s.validateDeviceForFlash(state, path, options.SkipBattery, options.AllowUnknownBattery, method))
	} else {
		addBlocker(s.ValidateDeviceForSideloadWithPolicy(ctx, state, path, options))
		if options.AllowUnknownBattery && errors.Is(CheckBattery(state), ErrBatteryUnknown) {
			plan.Warnings = append(plan.Warnings, "Battery status is unavailable; verify the device is sufficiently charged before sideloading.")
		}
	}
	if err := validateSelectedTarget(state); err != nil {
		addBlocker(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		addBlocker(fmt.Errorf("%w: input file cannot be accessed: %v", ErrFlashFailed, err))
	} else if !info.Mode().IsRegular() {
		addBlocker(fmt.Errorf("%w: input path is not a regular file", ErrFlashFailed))
	} else {
		if method != "adb-sideload" {
			fingerprint, fingerprintErr := CalculateInputFingerprint(path)
			if fingerprintErr != nil {
				addBlocker(fmt.Errorf("%w: input fingerprint failed: %v", ErrFlashFailed, fingerprintErr))
			} else {
				plan.InputFingerprintSHA256 = fingerprint
				if options.InputFingerprintSHA256 != "" &&
					!strings.EqualFold(fingerprint, strings.TrimSpace(options.InputFingerprintSHA256)) {
					addBlocker(fmt.Errorf("%w: selected %s, current %s", ErrInputChanged,
						options.InputFingerprintSHA256, fingerprint))
				}
			}
		}
		if options.ExpectedSHA256 == "" {
			if method == "adb-sideload" {
				plan.ChecksumStatus = "NOT CHECKED"
			} else {
				plan.Warnings = append(plan.Warnings, "SHA-256 expected checksum was not supplied; calculated hash is not trusted.")
			}
		} else {
			if err := VerifyImageSHA256(path, options.ExpectedSHA256); err != nil {
				plan.ChecksumStatus = "EXPECTED_MISMATCH"
				addBlocker(fmt.Errorf("checksum validation failed: %w", err))
			} else {
				plan.ChecksumStatus = "EXPECTED_MATCH"
			}
		}
		report := s.evaluateCompatibility(state, path, method)
		plan.CompatibilityStatus = string(report.State)
		if options.AllowCandidateCompatibility && options.RiskAcknowledged &&
			sideloadCompatibilityCanProceed(report, state) {
			plan.Warnings = append(plan.Warnings,
				"ROM compatibility is not fully verified. Confirm that this ZIP is intended for the selected device before continuing.")
		} else if !report.Allowed || report.State != CompatibilityVerified {
			addBlocker(fmt.Errorf("%w: %s", ErrCompatibility, report.Error()))
		} else if requiredPartition != "" && !hasIdentifier(report.RequiredPartitions, requiredPartition) {
			addBlocker(fmt.Errorf("%w: requested partition %q is not in the independently verified package partition requirements",
				ErrCompatibility, requiredPartition))
		}
	}
	if err := ctx.Err(); err != nil {
		addBlocker(fmt.Errorf("operation plan canceled: %w", err))
	}
	plan.err = errors.Join(blockers...)
	return plan
}

func sideloadCompatibilityCanProceed(report CompatibilityReport, device platform.DeviceState) bool {
	if report.State == CompatibilityVerified && report.Allowed {
		return true
	}
	if report.State == CompatibilityIncompatible || len(report.Conflicts) != 0 {
		return false
	}
	if len(report.PackageDeviceIDs) != 0 && strings.TrimSpace(device.Codename) != "" &&
		!hasIdentifier(report.PackageDeviceIDs, device.Codename) {
		return false
	}
	return report.State == CompatibilityCandidateMatch || report.State == CompatibilityUnknown
}

func (s *defaultFlashService) evaluateCompatibility(state platform.DeviceState, path, method string) CompatibilityReport {
	evaluator := s.compatibility
	if evaluator == nil {
		evaluator = CompatibilityEvaluatorFunc(EvaluateROMCompatibility)
	}
	return evaluator.Evaluate(state, path, method)
}

func runAuthorizedCommand(ctx context.Context, exec Executor, binary string, args ...string) error {
	return exec.RunCommand(authorizeCommand(ctx, binary, args...), binary, args...)
}

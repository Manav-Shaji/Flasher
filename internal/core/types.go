package core

import (
	"context"
	"errors"
	"flasher/internal/platform"
	"fmt"
)

var (
	ErrDeviceDisconnected     = errors.New("device disconnected")
	ErrBootloaderLocked       = errors.New("bootloader is locked")
	ErrHashMismatch           = errors.New("hash mismatch")
	ErrFlashFailed            = errors.New("flash failed")
	ErrBatteryLow             = errors.New("battery level too low")
	ErrBatteryUnknown         = errors.New("battery unknown")
	ErrBatteryInvalid         = errors.New("battery reading invalid")
	ErrCompatibility          = errors.New("ROM compatibility not established")
	ErrRiskAcknowledgement    = errors.New("explicit risk acknowledgement required")
	ErrOperationIndeterminate = errors.New("device state is indeterminate after an interrupted operation")
)

type FlashState int

const (
	StateIdle FlashState = iota
	StateValidating
	StatePreparing
	StateFlashing
	StateRebooting
	StateCompleted
	StateFailed
)

func (s FlashState) String() string {
	switch s {
	case StateIdle:
		return "Idle"
	case StateValidating:
		return "Validating"
	case StatePreparing:
		return "Preparing"
	case StateFlashing:
		return "Flashing"
	case StateRebooting:
		return "Rebooting"
	case StateCompleted:
		return "Completed"
	case StateFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// DeviceService defines operations for managing device state.
type DeviceService interface {
	PlanReboot(ctx context.Context, state platform.DeviceState, recovery, acknowledged bool) FlashPlan
	RebootSystem(ctx context.Context, state platform.DeviceState) error
	RebootRecovery(ctx context.Context, state platform.DeviceState) error
	RebootSystemWithAcknowledgement(ctx context.Context, state platform.DeviceState, acknowledged bool) error
	RebootRecoveryWithAcknowledgement(ctx context.Context, state platform.DeviceState, acknowledged bool) error
}

type defaultDeviceService struct {
	exec Executor
}

// NewDeviceService creates a new DeviceService.
func NewDeviceService(exec Executor) DeviceService {
	return &defaultDeviceService{exec: exec}
}

func (s *defaultDeviceService) RebootSystem(ctx context.Context, state platform.DeviceState) error {
	return s.RebootSystemWithAcknowledgement(ctx, state, false)
}

func (s *defaultDeviceService) RebootSystemWithAcknowledgement(ctx context.Context, state platform.DeviceState, acknowledged bool) error {
	plan := s.PlanReboot(ctx, state, false, acknowledged)
	if err := plan.Error(); err != nil {
		return err
	}
	if state.Mode == platform.ModeFastboot {
		return runAuthorizedCommand(ctx, s.exec, "fastboot", "-s", state.Serial, "reboot")
	}
	return runAuthorizedCommand(ctx, s.exec, "adb", "-s", state.Serial, "reboot")
}

func (s *defaultDeviceService) RebootRecovery(ctx context.Context, state platform.DeviceState) error {
	return s.RebootRecoveryWithAcknowledgement(ctx, state, false)
}

func (s *defaultDeviceService) RebootRecoveryWithAcknowledgement(ctx context.Context, state platform.DeviceState, acknowledged bool) error {
	plan := s.PlanReboot(ctx, state, true, acknowledged)
	if err := plan.Error(); err != nil {
		return err
	}
	if state.Mode == platform.ModeFastboot {
		return runAuthorizedCommand(ctx, s.exec, "fastboot", "-s", state.Serial, "reboot", "recovery")
	}
	return runAuthorizedCommand(ctx, s.exec, "adb", "-s", state.Serial, "reboot", "recovery")
}

func (s *defaultDeviceService) PlanReboot(ctx context.Context, state platform.DeviceState, recovery, acknowledged bool) FlashPlan {
	operation := "reboot system"
	if recovery {
		operation = "reboot recovery"
	}
	plan := FlashPlan{
		TargetSerial: state.Serial, TargetMode: state.Mode,
		TargetManufacturer: state.Manufacturer, TargetModel: state.Model,
		TargetCodename: state.Codename, Operation: operation,
		ChecksumStatus: "NOT APPLICABLE", CompatibilityStatus: "NOT APPLICABLE",
		BlockingReasons: []string{}, Warnings: []string{},
	}
	var blockers []error
	addBlocker := func(err error) {
		if err != nil {
			blockers = append(blockers, err)
			plan.BlockingReasons = append(plan.BlockingReasons, err.Error())
		}
	}
	if !acknowledged {
		addBlocker(ErrRiskAcknowledgement)
	}
	if !validRebootMode(state.Mode) {
		addBlocker(ErrDeviceDisconnected)
	}
	if err := validateSelectedTarget(state); err != nil {
		addBlocker(err)
	}
	if err := ctx.Err(); err != nil {
		addBlocker(fmt.Errorf("operation plan canceled: %w", err))
	}
	plan.err = errors.Join(blockers...)
	return plan
}

func validRebootMode(mode platform.DeviceMode) bool {
	switch mode {
	case platform.ModeFastboot, platform.ModeDevice, platform.ModeRecovery, platform.ModeSideload:
		return true
	default:
		return false
	}
}

package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/shlex"
	"go.uber.org/zap"

	"flasher/internal/platform"
)

type LogLevel string

const (
	LogInfo    LogLevel = "INFO"
	LogError   LogLevel = "ERROR"
	LogSuccess LogLevel = "SUCCESS"
)

type LogEntry struct {
	Level     LogLevel
	Text      string
	Timestamp time.Time
}

type OutputSink func(level LogLevel, line string)

// Executor defines how shell commands are executed.
// This allows the Engine to inject a UI-aware executor that streams logs.
type Executor interface {
	RunCommand(ctx context.Context, name string, args ...string) error
}

type LogMsg string
type ProgressMsg float64
type TaskCompleteMsg struct{ Err error }

var sensitiveCommandOutput = regexp.MustCompile(`(?i)(token|auth|secret|credential|private.?key)([^:]*:\s*).*`)

// Engine orchestrates background operations, streaming logs and managing cancellations.
type Engine struct {
	LogChan chan string
	logger  *zap.Logger
	sinkMu  sync.RWMutex
	sink    OutputSink

	FlashService           FlashService
	DeviceService          DeviceService
	CompatibilityEvaluator CompatibilityEvaluator
	CommandTimeout         time.Duration

	cmdMu           sync.Mutex
	activeCmdCancel context.CancelFunc
}

// NewEngine creates a new core Engine.
func NewEngine(logger *zap.Logger) *Engine {
	if logger == nil {
		logger = zap.NewNop()
	}
	if err := platform.ExtractEmbeddedBinaries(); err != nil {
		logger.Warn("Failed to extract embedded binaries", zap.Error(err))
	}

	evaluator := CompatibilityEvaluatorFunc(EvaluateROMCompatibility)
	e := &Engine{
		LogChan:                make(chan string, 200),
		logger:                 logger,
		sink:                   func(LogLevel, string) {},
		CommandTimeout:         30 * time.Minute,
		CompatibilityEvaluator: evaluator,
	}

	e.FlashService = NewFlashServiceWithCompatibilityEvaluator(e, evaluator)
	e.DeviceService = NewDeviceService(e)

	return e
}

func (e *Engine) SetCompatibilityEvaluator(evaluator CompatibilityEvaluator) {
	if evaluator == nil {
		evaluator = CompatibilityEvaluatorFunc(EvaluateROMCompatibility)
	}
	e.CompatibilityEvaluator = evaluator
	e.FlashService = NewFlashServiceWithCompatibilityEvaluator(e, evaluator)
}

func (e *Engine) EvaluateCompatibility(device platform.DeviceState, path, method string) CompatibilityReport {
	evaluator := e.CompatibilityEvaluator
	if evaluator == nil {
		evaluator = CompatibilityEvaluatorFunc(EvaluateROMCompatibility)
	}
	return evaluator.Evaluate(device, path, method)
}

func (e *Engine) SetSink(sink OutputSink) {
	if sink == nil {
		sink = func(LogLevel, string) {}
	}
	e.sinkMu.Lock()
	e.sink = sink
	e.sinkMu.Unlock()
}

func (e *Engine) writeOutput(level LogLevel, line string) {
	e.sinkMu.RLock()
	sink := e.sink
	e.sinkMu.RUnlock()
	sink(level, line)
}

func (e *Engine) Emit(level LogLevel, line string) {
	e.writeOutput(level, line)
}

// CancelActiveCommand allows the UI or CLI to cancel the currently running background command.
func (e *Engine) CancelActiveCommand() {
	e.cmdMu.Lock()
	defer e.cmdMu.Unlock()
	if e.activeCmdCancel != nil {
		e.activeCmdCancel()
		e.logger.Info("User initiated command cancellation")
	}
}

// WaitForLogs creates a tea.Cmd that blocks until a log message is available.
func (e *Engine) WaitForLogs() tea.Cmd {
	return func() tea.Msg { return LogMsg(<-e.LogChan) }
}

// ExecuteAsync wraps a context-aware service action into a Bubble Tea command.
func (e *Engine) ExecuteAsync(action func(context.Context) error) tea.Cmd {
	return func() tea.Msg {
		e.cmdMu.Lock()
		if e.activeCmdCancel != nil {
			e.cmdMu.Unlock()
			return TaskCompleteMsg{Err: fmt.Errorf("another device operation is already running")}
		}
		ctx, cancel := context.WithCancel(context.Background())
		e.activeCmdCancel = cancel
		e.cmdMu.Unlock()

		defer func() {
			e.cmdMu.Lock()
			e.activeCmdCancel = nil
			e.cmdMu.Unlock()
			cancel()
		}()

		err := action(ctx)
		return TaskCompleteMsg{Err: err}
	}
}

// RunCommand implements Executor, running raw shell commands and piping output.
func (e *Engine) RunCommand(ctx context.Context, name string, args ...string) error {
	allowedCmds := map[string]bool{"adb": true, "fastboot": true}
	if !allowedCmds[name] {
		err := fmt.Errorf("security violation: execution of binary '%s' is not permitted", name)
		e.logger.Error("Command rejected by security policy", zap.String("name", name))
		return err
	}
	risk, reason := ClassifyCommand(name, args)
	if risk == RiskBlocked {
		e.writeOutput(LogError, reason)
		return fmt.Errorf("command blocked: %s", reason)
	}
	if risk != RiskSafe && !commandAuthorized(ctx, name, args) {
		err := fmt.Errorf("device-changing command rejected: use a validated core operation with explicit risk acknowledgement")
		e.writeOutput(LogError, err.Error())
		e.logger.Warn("Unvalidated device-changing command rejected",
			zap.String("binary", name), zap.Strings("args", redactCommandTarget(args)))
		return err
	}

	e.logger.Debug("Resolving command path", zap.String("name", name))
	resolvedPath, err := platform.ResolveCommandPath(name)
	if err != nil {
		msg := fmt.Sprintf("CRITICAL ERROR: %s", err.Error())
		e.writeOutput(LogError, msg)
		e.logger.Error("Binary not found", zap.String("name", name), zap.Error(err))
		return fmt.Errorf("action failed: %w", err)
	}
	bin := name

	pwd, _ := os.Getwd()
	displayCmd := bin
	displayArgs := redactCommandTarget(args)

	cmdPrompt := fmt.Sprintf("%s>%s", pwd, displayCmd)
	for _, a := range displayArgs {
		cmdPrompt += " " + a
	}

	e.writeOutput(LogInfo, cmdPrompt)
	e.writeOutput(LogInfo, "STARTING COMMAND EXECUTION...")
	e.logger.Info("Executing command", zap.String("command", cmdPrompt))

	// Ensure there is a hard timeout if the context doesn't already have one
	var cancel context.CancelFunc
	if _, ok := ctx.Deadline(); !ok {
		timeout := e.CommandTimeout
		if timeout <= 0 {
			timeout = 30 * time.Minute
		}
		if isQuickQuery(name, args) {
			timeout = 10 * time.Second
		}
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := buildCommand(ctx, bin, resolvedPath, args)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to open command stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdout.Close()
		return fmt.Errorf("failed to open command stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		e.writeOutput(LogError, fmt.Sprintf("FAILED START: %v", err))
		e.logger.Error("Failed to start command", zap.Error(err))
		return fmt.Errorf("action failed: %w", err)
	}

	e.writeOutput(LogInfo, "STREAMERS ATTACHED. WAITING FOR OUTPUT...")

	var wg sync.WaitGroup
	wg.Add(2)

	stream := func(r io.ReadCloser, level LogLevel) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Split(func(data []byte, atEOF bool) (advance int, token []byte, err error) {
			if atEOF && len(data) == 0 {
				return 0, nil, nil
			}
			for i := 0; i < len(data); i++ {
				if data[i] == '\n' || data[i] == '\r' {
					return i + 1, data[0 : i+1], nil
				}
			}
			if atEOF {
				return len(data), append(data, '\n'), nil
			}
			return 0, nil, nil
		})

		for sc.Scan() {
			token := sc.Bytes()
			if len(token) == 0 {
				continue
			}

			isCR := token[len(token)-1] == '\r'
			token = token[:len(token)-1]
			if len(token) == 0 {
				continue
			}

			line := string(token)
			if isCR {
				line = "\r" + line
			}
			line = redactCommandOutput(sanitizeCommandOutput(line))

			e.writeOutput(level, line)
			e.logger.Debug("Process Output", zap.String("line", line))
		}
		if err := sc.Err(); err != nil {
			e.writeOutput(LogError, fmt.Sprintf("scanner error: %v", err))
			e.logger.Warn("Scanner error", zap.Error(err))
		}
	}

	go stream(stdout, LogInfo)
	go stream(stderr, LogError)

	startExec := time.Now()
	wg.Wait()
	err = cmd.Wait()
	elapsed := time.Since(startExec)

	if ctx.Err() == context.Canceled {
		if risk != RiskSafe {
			e.writeOutput(LogError, "INTERRUPTED: device state is indeterminate; no rollback is implied.")
			e.logger.Warn("Device-changing command was canceled; outcome is indeterminate")
			return indeterminateCommandError(risk, "was canceled", ctx.Err())
		}
		e.writeOutput(LogError, "ABORTED: Task was canceled by user.")
		e.logger.Warn("Command was canceled via context")
		return fmt.Errorf("task canceled: %w", context.Canceled)
	}

	if ctx.Err() == context.DeadlineExceeded {
		if risk != RiskSafe {
			e.writeOutput(LogError, "INTERRUPTED: device state is indeterminate after timeout; no rollback is implied.")
			e.logger.Warn("Device-changing command timed out; outcome is indeterminate")
			return indeterminateCommandError(risk, "timed out", ctx.Err())
		}
		e.writeOutput(LogError, "TIMEOUT: Task exceeded the time limit.")
		e.logger.Warn("Command exceeded timeout limit")
		return fmt.Errorf("task timed out: %w", context.DeadlineExceeded)
	}

	if err != nil {
		e.writeOutput(LogError, fmt.Sprintf("EXECUTION ERROR: %v", err))
		e.logger.Error("Command execution returned error", zap.Error(err), zap.Duration("elapsed", elapsed))
		if risk != RiskSafe {
			e.writeOutput(LogError, "DEVICE STATE INDETERMINATE: device-changing command exited with an error; no rollback is implied.")
			e.logger.Warn("Device-changing command exited with an error; outcome is indeterminate",
				zap.Error(err), zap.Duration("elapsed", elapsed))
			return indeterminateCommandError(risk, "exited with an error", err)
		}
		return fmt.Errorf("action failed: %w", err)
	}
	e.writeOutput(LogSuccess, fmt.Sprintf("SUCCESS: Task finished successfully in %v.", elapsed.Round(time.Millisecond)))
	e.logger.Info("Command executed successfully", zap.Duration("elapsed", elapsed))
	return nil
}

func indeterminateCommandError(risk CommandRisk, outcome string, cause error) error {
	if risk == RiskSafe {
		return cause
	}
	return fmt.Errorf("%w: device-changing command %s: %w", ErrOperationIndeterminate, outcome, cause)
}

func redactCommandTarget(args []string) []string {
	result := append([]string(nil), args...)
	for i := 0; i+1 < len(result); i++ {
		if result[i] == "-s" {
			result[i+1] = "[selected device]"
			i++
		}
	}
	return result
}

func redactCommandOutput(line string) string {
	if sensitiveCommandOutput.MatchString(line) {
		return sensitiveCommandOutput.ReplaceAllString(line, "$1$2[REDACTED]")
	}
	return line
}

func sanitizeCommandOutput(line string) string {
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) && char != '\r' && char != '\t' {
			return -1
		}
		return char
	}, line)
}

func isQuickQuery(name string, args []string) bool {
	args = skipGlobalFlags(name, args)
	if len(args) == 0 {
		return false
	}
	switch name {
	case "fastboot":
		return args[0] == "devices" || args[0] == "getvar" || args[0] == "--version" || args[0] == "version"
	case "adb":
		switch args[0] {
		case "devices", "version", "get-state", "get-serialno", "get-devpath":
			return true
		case "logcat":
			return len(args) > 1 && args[1] == "-d"
		}
	}
	return false
}

func buildCommand(ctx context.Context, bin, resolved string, args []string) *exec.Cmd {
	cmdArgs := args
	if bin == "adb" {
		if pathIndex := adbSideloadPathIndex(args); pathIndex >= 0 {
			cmdArgs = append([]string(nil), args...)
			path := args[pathIndex]
			cmdArgs[pathIndex] = filepath.Base(path)
			cmd := exec.CommandContext(ctx, resolved, cmdArgs...)
			cmd.Dir = filepath.Dir(path)
			return cmd
		}
	}
	return exec.CommandContext(ctx, resolved, cmdArgs...)
}

func adbSideloadPathIndex(args []string) int {
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-s", "-H", "-P", "-t":
			i += 2
		case "-d", "-e", "-a":
			i++
		default:
			if args[i] == "sideload" && i+1 < len(args) {
				return i + 1
			}
			return -1
		}
	}
	return -1
}

func ParseCustomCommand(cmdStr string) (string, []string, error) {
	fields, err := shlex.Split(cmdStr)
	if err != nil {
		return "", nil, fmt.Errorf("invalid command: %w", err)
	}
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("command is empty; start with adb or fastboot")
	}
	if fields[0] != "adb" && fields[0] != "fastboot" {
		return "", nil, fmt.Errorf("start with adb or fastboot")
	}
	return fields[0], fields[1:], nil
}

func ParseCustomCommandForMode(cmdStr string, mode platform.DeviceMode) (string, []string, string, error) {
	defaultBin := ""
	switch mode {
	case platform.ModeFastboot:
		defaultBin = "fastboot"
	case platform.ModeDevice, platform.ModeRecovery, platform.ModeSideload:
		defaultBin = "adb"
	default:
		return "", nil, "", fmt.Errorf("connect a device in Fastboot, Android, or Recovery mode first")
	}

	fields, err := shlex.Split(cmdStr)
	if err != nil {
		return "", nil, "", fmt.Errorf("invalid command: %w", err)
	}
	if len(fields) == 0 {
		return "", nil, "", fmt.Errorf("command is empty")
	}

	bin := defaultBin
	display := defaultBin + " " + strings.TrimSpace(cmdStr)
	if fields[0] == "adb" || fields[0] == "fastboot" {
		if fields[0] != defaultBin {
			return "", nil, "", fmt.Errorf("%s connection requires %s commands", mode, defaultBin)
		}
		bin = fields[0]
		fields = fields[1:]
		display = strings.TrimSpace(cmdStr)
	}
	if len(fields) == 0 {
		return "", nil, "", fmt.Errorf("command is empty")
	}
	return bin, fields, display, nil
}

func (e *Engine) RunCustomCommand(ctx context.Context, mode platform.DeviceMode, serial, cmdStr string) error {
	bin, args, _, err := ParseCustomCommandForMode(cmdStr, mode)
	if err != nil {
		return err
	}
	if risk, reason := ClassifyCommand(bin, args); risk != RiskSafe {
		return fmt.Errorf("custom commands are limited to recognized read-only operations: %s", reason)
	}
	if serial == "" || serial == "-" {
		return fmt.Errorf("selected device has no usable serial number")
	}
	for _, arg := range args {
		switch arg {
		case "-s", "--serial", "-d", "-e", "-t":
			return fmt.Errorf("custom commands cannot override the selected device")
		}
	}
	if err := platform.ValidateDeviceTarget(serial, mode); err != nil {
		return err
	}
	targetedArgs := make([]string, 0, len(args)+2)
	targetedArgs = append(targetedArgs, "-s", serial)
	targetedArgs = append(targetedArgs, args...)
	return e.RunCommand(ctx, bin, targetedArgs...)
}

func (e *Engine) RunCustomFastbootCommand(ctx context.Context, serial string, args []string) error {
	if !IsAllowedCustomFastbootCommand(args) {
		return fmt.Errorf("custom fastboot command is not allowed")
	}
	if err := platform.ValidateDeviceTarget(serial, platform.ModeFastboot); err != nil {
		return err
	}
	targetedArgs := make([]string, 0, len(args)+2)
	targetedArgs = append(targetedArgs, "-s", serial)
	targetedArgs = append(targetedArgs, args...)
	return runAuthorizedCommand(ctx, e, "fastboot", targetedArgs...)
}

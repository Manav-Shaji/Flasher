package config

import (
	"flasher/internal/core"
	"flasher/internal/platform"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

/* CONFIG MODELS */

type PickerConfig struct {
	Title  string `json:"title" mapstructure:"title"`
	Filter string `json:"filter" mapstructure:"filter"`
	SubDir string `json:"sub_dir" mapstructure:"sub_dir"`
}

type MenuConfig struct {
	Label  string        `json:"label" mapstructure:"label"`
	Icon   string        `json:"icon" mapstructure:"icon"`
	Desc   string        `json:"desc" mapstructure:"desc"`
	Action string        `json:"action" mapstructure:"action"`
	Picker *PickerConfig `json:"picker,omitempty" mapstructure:"picker"`
}

type LogConfig struct {
	Level    string `json:"level" mapstructure:"level"`
	Format   string `json:"format" mapstructure:"format"`
	Dir      string `json:"dir" mapstructure:"dir"`
	Filename string `json:"filename" mapstructure:"filename"`
}

type AppConfig struct {
	BaseDir               string            `json:"base_dir" mapstructure:"base_dir"`
	DevicePath            string            `json:"device_path,omitempty" mapstructure:"device_path"`
	PlatformToolsDir      string            `json:"platform_tools_dir,omitempty" mapstructure:"platform_tools_dir"`
	CommandTimeoutMinutes int               `json:"command_timeout_minutes" mapstructure:"command_timeout_minutes"`
	DeviceNames           map[string]string `json:"device_names,omitempty" mapstructure:"device_names"`
	ASCII                 bool              `json:"ascii" mapstructure:"ascii"`
	Folders               map[string]string `json:"folders" mapstructure:"folders"`
	Log                   LogConfig         `json:"log" mapstructure:"log"`
}

// Load loads the AppConfig from the filesystem via Viper
func defaultBaseDir() string {
	home, err := os.UserHomeDir()
	if err == nil {
		downloadsDir := filepath.Join(home, "Downloads")
		if info, statErr := os.Stat(downloadsDir); statErr == nil && info.IsDir() {
			return downloadsDir
		}
	}

	if pwd, err := os.Getwd(); err == nil {
		return pwd
	}
	return "."
}

func defaultLogDir() string {
	baseDir := os.Getenv("XDG_STATE_HOME")
	if runtime.GOOS == "windows" {
		baseDir = os.Getenv("LOCALAPPDATA")
	} else if baseDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			baseDir = filepath.Join(home, ".local", "state")
		}
	}
	if baseDir == "" {
		var err error
		baseDir, err = os.UserCacheDir()
		if err != nil {
			baseDir = ""
		}
	}
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	return filepath.Join(baseDir, "flasher", "logs")
}

func userConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "flasher", "config.json"), nil
}

func Load() (*AppConfig, error) {
	v := viper.New()

	// Set Defaults
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("log.dir", defaultLogDir())
	v.SetDefault("log.filename", "app.log")
	v.SetDefault("base_dir", defaultBaseDir())
	v.SetDefault("ascii", false)
	v.SetDefault("command_timeout_minutes", 30)
	v.SetDefault("platform_tools_dir", "")
	v.SetDefault("device_names", map[string]string{})
	v.SetDefault("folders", map[string]string{})

	// Setup Env variables (e.g. FLASHER_LOG_LEVEL)
	v.SetEnvPrefix("FLASHER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	configPath, err := userConfigPath()
	if err != nil {
		return nil, err
	}
	configPaths := []string{
		configPath,
		filepath.Join(".", "config.json"),
		filepath.Join(".", "config", "config.json"),
	}
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		configPaths = append(configPaths, filepath.Join(home, ".flasher", "config.json"))
	}
	for _, path := range configPaths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("failed to access config %s: %w", path, err)
		}
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read config %s: %w", path, err)
		}
		break
	}

	normalizeCommandTimeout(v)

	// Unmarshal into Struct
	var cfg AppConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	if cfg.Folders == nil {
		cfg.Folders = make(map[string]string)
	}
	if cfg.DeviceNames == nil {
		cfg.DeviceNames = make(map[string]string)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

func normalizeCommandTimeout(v *viper.Viper) {
	const defaultMinutes = 30
	timeout := defaultMinutes
	switch value := v.Get("command_timeout_minutes").(type) {
	case int:
		timeout = value
	case int64:
		if value >= 1 && value <= 240 {
			timeout = int(value)
		}
	case float64:
		if value >= 1 && value <= 240 && value == float64(int(value)) {
			timeout = int(value)
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			timeout = parsed
		}
	}
	if timeout < 1 || timeout > 240 {
		timeout = defaultMinutes
	}
	v.Set("command_timeout_minutes", timeout)
}

// Validate ensures configuration fields have sensible defaults and are valid
func (c *AppConfig) Validate() error {
	if c.BaseDir == "" {
		c.BaseDir = defaultBaseDir()
	}
	if c.CommandTimeoutMinutes < 1 || c.CommandTimeoutMinutes > 240 {
		c.CommandTimeoutMinutes = 30
	}
	if c.PlatformToolsDir != "" {
		if !filepath.IsAbs(c.PlatformToolsDir) {
			return fmt.Errorf("platform_tools_dir must be an absolute path")
		}
		info, err := os.Stat(c.PlatformToolsDir)
		if err != nil {
			return fmt.Errorf("platform_tools_dir: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("platform_tools_dir must be a directory")
		}
	}

	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[strings.ToLower(c.Log.Level)] {
		c.Log.Level = "info"
	}

	if c.Log.Dir == "" {
		c.Log.Dir = defaultLogDir()
	}
	if c.Log.Filename == "" {
		c.Log.Filename = "app.log"
	}

	if err := os.MkdirAll(c.Log.Dir, 0755); err != nil {
		return fmt.Errorf("create log directory %s: %w", c.Log.Dir, err)
	}

	return nil
}

// SaveConfig saves the AppConfig to the filesystem (legacy support for TUI Settings)
func SaveConfig(cfg *AppConfig) error {
	if cfg == nil {
		return fmt.Errorf("config cannot be nil")
	}
	v := viper.New()
	v.Set("base_dir", cfg.BaseDir)
	v.Set("device_path", cfg.DevicePath)
	v.Set("platform_tools_dir", cfg.PlatformToolsDir)
	v.Set("command_timeout_minutes", cfg.CommandTimeoutMinutes)
	v.Set("device_names", cfg.DeviceNames)
	v.Set("ascii", cfg.ASCII)
	v.Set("folders", cfg.Folders)
	v.Set("log", cfg.Log)

	path, err := userConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	if err := v.WriteConfigAs(path); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	return nil
}

// App is the central dependency injection container for the CLI and TUI.
// It holds all active, initialized infrastructure required for the program to execute.
type App struct {
	Config *AppConfig
	Logger *zap.Logger
	Engine *core.Engine
}

// New creates a new Application container.
func New(cfg *AppConfig, logger *zap.Logger, engineInstance *core.Engine) *App {
	return &App{
		Config: cfg,
		Logger: logger,
		Engine: engineInstance,
	}
}

// Initialize executes the core startup sequence of the application.
// It loads configuration, initializes the logger, sets up the engine,
// and returns the fully prepared runtime application container.
func Initialize() (*App, error) {
	// Load Configuration (Viper)
	cfg, err := Load()
	if err != nil {
		return nil, fmt.Errorf("bootstrap config failed: %w", err)
	}
	platform.SetPlatformToolsDir(cfg.PlatformToolsDir)
	platform.SetDeviceNames(cfg.DeviceNames)

	// Initialize Logger (Zap)
	log, err := InitLogger(cfg)
	if err != nil {
		return nil, fmt.Errorf("bootstrap logger failed: %w", err)
	}

	log.Info("Starting Flasher initialization sequence")

	// Initialize Core Engine
	engineInstance := core.NewEngine(log.Named("engine"))
	engineInstance.CommandTimeout = time.Duration(cfg.CommandTimeoutMinutes) * time.Minute

	// Create Application Container
	applicationContainer := New(cfg, log, engineInstance)

	log.Info("Flasher initialization sequence complete")
	return applicationContainer, nil
}

// InitLogger creates a new Zap logger based on the provided configuration.
func InitLogger(cfg *AppConfig) (*zap.Logger, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// 1. Ensure log directory exists
	logDir := cfg.Log.Dir
	if logDir == "" {
		logDir = "logs"
	}
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	logFile := cfg.Log.Filename
	if logFile == "" {
		logFile = "app.log"
	}
	logPath := filepath.Join(logDir, logFile)

	// 2. Parse Log Level
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(cfg.Log.Level))); err != nil {
		level = zap.InfoLevel
	}

	// 3. Configure Encoder (JSON vs Console)
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	var encoder zapcore.Encoder
	if strings.ToLower(cfg.Log.Format) == "console" {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	// 4. Setup Log Output (File)
	// Note: In a CLI/TUI app, logging to stdout directly will corrupt the UI.
	// Therefore, we strictly log to the file.
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %w", logPath, err)
	}

	writer := zapcore.AddSync(file)
	core := zapcore.NewCore(encoder, writer, level)

	logger := zap.New(core, zap.AddCaller())
	return logger, nil
}

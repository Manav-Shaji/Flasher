package platform

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

//go:embed bin/*
var embeddedBinaries embed.FS

var (
	ExtractedBinDir  string
	platformToolsDir string
	binPathCache     = map[string]string{}
	lookPath         = exec.LookPath
	userCacheDirFn   = os.UserCacheDir
	createTempDirFn  = func() (string, error) { return os.MkdirTemp("", "flasher-bin-*") }
	binPathMu        sync.RWMutex
)

func ExtractEmbeddedBinaries() error {
	cacheDir, cacheErr := userCacheDirFn()
	var tmpDir string
	if cacheErr == nil && cacheDir != "" {
		tmpDir = filepath.Join(cacheDir, "flasher", "bin")
		if err := os.MkdirAll(tmpDir, 0700); err == nil {
			if err := validateExtractDir(tmpDir); err != nil {
				return err
			}
		} else {
			tmpDir = ""
		}
	}
	if tmpDir == "" {
		var err error
		tmpDir, err = createTempDirFn()
		if err != nil {
			return fmt.Errorf("create private temporary binary directory: %w", err)
		}
		if err := validateExtractDir(tmpDir); err != nil {
			return err
		}
	}

	entries, err := embeddedBinaries.ReadDir("bin")
	if err != nil {
		// No bin directory or it's empty
		return nil
	}

	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "README.md" {
			continue
		}

		inPath := filepath.Join("bin", entry.Name())
		outPath := filepath.Join(tmpDir, entry.Name())

		// Read from embed
		data, err := embeddedBinaries.ReadFile(inPath)
		if err != nil {
			return err
		}

		// Hash comparison
		if stat, err := os.Lstat(outPath); err == nil {
			if stat.Mode()&os.ModeSymlink != 0 || !stat.Mode().IsRegular() {
				return fmt.Errorf("embedded binary target is not a regular file: %s", outPath)
			}
			if f, err := os.Open(outPath); err == nil {
				h := sha256.New()
				if _, err := io.Copy(h, f); err == nil {
					f.Close()
					diskHash := h.Sum(nil)
					embedHash := sha256.Sum256(data)
					if bytes.Equal(diskHash, embedHash[:]) {
						if err := os.Chmod(outPath, 0700); err != nil {
							return err
						}
						continue // Skips disk write if already matching
					}
				} else {
					f.Close()
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		}

		// Write to temp dir
		if err := os.WriteFile(outPath, data, 0700); err != nil {
			return err
		}
		if err := os.Chmod(outPath, 0700); err != nil {
			return err
		}
	}

	ExtractedBinDir = tmpDir
	binPathMu.Lock()
	binPathCache = map[string]string{}
	binPathMu.Unlock()
	return nil
}

func validateExtractDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect embedded binary directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("embedded binary directory is not a real directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("secure embedded binary directory: %w", err)
	}
	return verifyDirectoryOwner(dir)
}

func GetBinaryPath(name string) string {
	binPathMu.RLock()
	if p, ok := binPathCache[name]; ok {
		binPathMu.RUnlock()
		return p
	}
	binPathMu.RUnlock()

	if ExtractedBinDir == "" {
		binPathMu.Lock()
		binPathCache[name] = ""
		binPathMu.Unlock()
		return ""
	}
	binPath := filepath.Join(ExtractedBinDir, name)
	if isRegularFile(binPath) {
		binPathMu.Lock()
		binPathCache[name] = binPath
		binPathMu.Unlock()
		return binPath
	}

	binPathMu.Lock()
	binPathCache[name] = ""
	binPathMu.Unlock()
	return ""
}

func SetPlatformToolsDir(dir string) {
	binPathMu.Lock()
	platformToolsDir = dir
	binPathCache = map[string]string{}
	binPathMu.Unlock()
}

// ResolveCommandPath checks embedded binaries, the configured tools directory, then PATH.
func ResolveCommandPath(name string) (string, error) {
	binPathMu.RLock()
	configuredDir := platformToolsDir
	binPathMu.RUnlock()
	return ResolveCommandPathWithToolsDir(name, configuredDir)
}

func ResolveCommandPathWithToolsDir(name, toolsDir string) (string, error) {
	binPathMu.RLock()
	if p, ok := binPathCache[name]; ok && p != "" {
		binPathMu.RUnlock()
		return p, nil
	}
	binPathMu.RUnlock()

	if binPath := GetBinaryPath(name); binPath != "" {
		return binPath, nil
	} else if runtime.GOOS == "windows" {
		winName := name + ".exe"
		if binPath := GetBinaryPath(winName); binPath != "" {
			return binPath, nil
		}
	}

	if toolsDir != "" {
		candidate := filepath.Join(toolsDir, name)
		if runtime.GOOS == "windows" {
			candidate += ".exe"
		}
		if isRegularFile(candidate) {
			return candidate, nil
		}
	}

	if path, err := lookPath(name); err == nil {
		if isRegularFile(path) {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s not found in embedded binaries, platform_tools_dir, or PATH", name)
}

func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

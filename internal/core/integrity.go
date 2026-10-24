package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxSHA256SidecarSize = 4096

var fingerprintBufferPool = sync.Pool{
	New: func() any { return make([]byte, 1<<20) },
}

// Checksum verification is a CLI preflight, so it does not need a flash state.
func VerifyImageSHA256(path, expected string) error {
	expected = strings.TrimSpace(expected)
	if len(expected) != sha256.Size*2 {
		return fmt.Errorf("invalid SHA-256 checksum length")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		return fmt.Errorf("invalid SHA-256 checksum: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open image for checksum: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("read image for checksum: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("%w: expected %s, got %s", ErrHashMismatch, expected, actual)
	}
	return nil
}

// CalculateInputFingerprint identifies the bytes currently read from path. It
// is an identity check only; it does not establish who produced those bytes.
func CalculateInputFingerprint(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open input for fingerprint: %w", err)
	}
	defer file.Close()

	hash := sha256.New()
	buffer := fingerprintBufferPool.Get().([]byte)
	defer fingerprintBufferPool.Put(buffer)
	if _, err := io.CopyBuffer(hash, struct{ io.Reader }{file}, buffer); err != nil {
		return "", fmt.Errorf("read input for fingerprint: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ReadSHA256ChecksumFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSHA256SidecarSize+1))
	closeErr := file.Close()
	if err != nil {
		return "", fmt.Errorf("read checksum file: %w", err)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close checksum file: %w", closeErr)
	}
	if len(data) > maxSHA256SidecarSize {
		return "", fmt.Errorf("checksum file exceeds %d bytes", maxSHA256SidecarSize)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("checksum file is empty")
	}
	return fields[0], nil
}

var ErrInputChanged = errors.New("input content changed after selection or preflight")

func stageInputSnapshot(ctx context.Context, source, expectedFingerprint string) (string, string, func(), error) {
	return stageInputSnapshotWithHash(ctx, source, expectedFingerprint, true)
}

func stageInputSnapshotWithoutHash(ctx context.Context, source string) (string, func(), error) {
	path, _, cleanup, err := stageInputSnapshotWithHash(ctx, source, "", false)
	return path, cleanup, err
}

func stageInputSnapshotWithHash(
	ctx context.Context,
	source, expectedFingerprint string,
	calculateHash bool,
) (string, string, func(), error) {
	if !calculateHash && expectedFingerprint != "" {
		return "", "", nil, fmt.Errorf("cannot verify an input fingerprint when hashing is disabled")
	}
	if expectedFingerprint == "" {
		if calculateHash {
			var err error
			expectedFingerprint, err = CalculateInputFingerprint(source)
			if err != nil {
				return "", "", nil, fmt.Errorf("fingerprint input: %w", err)
			}
		}
	}
	if calculateHash {
		expectedFingerprint = strings.ToLower(strings.TrimSpace(expectedFingerprint))
		if len(expectedFingerprint) != 64 {
			return "", "", nil, fmt.Errorf("invalid input content fingerprint")
		}
		if _, err := hex.DecodeString(expectedFingerprint); err != nil {
			return "", "", nil, fmt.Errorf("invalid input content fingerprint: %w", err)
		}
	}

	sourceFile, err := os.Open(source)
	if err != nil {
		return "", "", nil, fmt.Errorf("open input for staging: %w", err)
	}
	defer sourceFile.Close()
	info, err := sourceFile.Stat()
	if err != nil {
		return "", "", nil, fmt.Errorf("inspect input for staging: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", nil, fmt.Errorf("%w: input path is not a regular file", ErrFlashFailed)
	}

	extension := filepath.Ext(source)
	if len(extension) > 16 {
		extension = ""
	}
	privateDir, err := os.MkdirTemp("", "flasher-input-")
	if err != nil {
		return "", "", nil, fmt.Errorf("create private input snapshot directory: %w", err)
	}
	if err := os.Chmod(privateDir, 0700); err != nil {
		_ = os.Remove(privateDir)
		return "", "", nil, fmt.Errorf("protect input snapshot directory: %w", err)
	}
	staged, err := os.CreateTemp(privateDir, "input-*"+extension)
	if err != nil {
		_ = os.Remove(privateDir)
		return "", "", nil, fmt.Errorf("create protected input snapshot: %w", err)
	}
	stagedPath := staged.Name()
	cleanup := func() {
		_ = os.Remove(stagedPath)
		_ = os.Remove(privateDir)
	}
	if err := staged.Chmod(0600); err != nil {
		_ = staged.Close()
		cleanup()
		return "", "", nil, fmt.Errorf("protect input snapshot: %w", err)
	}

	var hash io.Writer = staged
	var hasher interface {
		Write([]byte) (int, error)
		Sum([]byte) []byte
	}
	if calculateHash {
		hasher = sha256.New()
		hash = io.MultiWriter(staged, hasher)
	}
	written, copyErr := io.Copy(hash, contextReader{ctx: ctx, reader: sourceFile})
	if copyErr != nil {
		_ = staged.Close()
		cleanup()
		return "", "", nil, fmt.Errorf("stage input snapshot: %w", copyErr)
	}
	if err := ctx.Err(); err != nil {
		_ = staged.Close()
		cleanup()
		return "", "", nil, err
	}
	if written != info.Size() {
		_ = staged.Close()
		cleanup()
		return "", "", nil, fmt.Errorf("%w: input size changed while staging", ErrInputChanged)
	}
	actualFingerprint := ""
	if calculateHash {
		actualFingerprint = hex.EncodeToString(hasher.Sum(nil))
		if actualFingerprint != expectedFingerprint {
			_ = staged.Close()
			cleanup()
			return "", "", nil, fmt.Errorf("%w: selected %s, staged %s",
				ErrInputChanged, expectedFingerprint, actualFingerprint)
		}
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		cleanup()
		return "", "", nil, fmt.Errorf("flush input snapshot: %w", err)
	}
	if err := staged.Close(); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("close input snapshot: %w", err)
	}
	return stagedPath, actualFingerprint, cleanup, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

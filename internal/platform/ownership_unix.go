//go:build !windows

package platform

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

func verifyDirectoryOwner(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine embedded binary directory owner")
	}
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("resolve current user: %w", err)
	}
	uid, err := strconv.ParseUint(current.Uid, 10, 32)
	if err != nil {
		return fmt.Errorf("parse current user ID: %w", err)
	}
	if uint64(stat.Uid) != uid {
		return fmt.Errorf("embedded binary directory is not owned by the current user")
	}
	return nil
}

//go:build windows

package platform

func verifyDirectoryOwner(string) error {
	return nil
}

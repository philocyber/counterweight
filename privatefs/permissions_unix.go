//go:build !windows && !linux

package privatefs

import "os"

func protectOpenedFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm() == 0600 {
		return nil
	}
	return f.Chmod(0600)
}

func protectOwnerFile(path string, mode os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm() == mode {
		return nil
	}
	return os.Chmod(path, mode)
}

func protect(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm() == mode {
		// Already-private read-only mounts do not require a chmod operation.
		return nil
	}
	return os.Chmod(path, mode)
}

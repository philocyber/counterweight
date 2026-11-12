//go:build !windows && !linux

package privatefs

import "os"

func openExistingPrivateWrite(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	// Preserve legacy owner-mode repair on platforms without the native Linux
	// inspection path. This preliminary permission repair remains path-based;
	// the opened identity is checked before any truncation below.
	if err := ProtectFile(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		f.Close()
		if err == nil {
			err = os.ErrInvalid
		}
		return nil, err
	}
	return f, nil
}

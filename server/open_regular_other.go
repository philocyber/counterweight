//go:build !unix

package server

import "os"

// openRegularFile opens path and returns the handle with its Stat taken on the
// opened object. A non-regular object is refused. With noFollow a link in the
// final component is refused before the open; callers that must rule out a swap
// compare the result with os.SameFile. O_TRUNC is applied only after the type
// check, so a refused object is never modified.
func openRegularFile(path string, flag int, perm os.FileMode, noFollow bool) (*os.File, os.FileInfo, error) {
	if noFollow {
		if info, err := os.Lstat(path); err != nil {
			return nil, nil, err
		} else if info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, errNotRegularFile
		}
	}
	truncate := flag&os.O_TRUNC != 0
	f, err := os.OpenFile(path, flag&^os.O_TRUNC, perm)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, errNotRegularFile
	}
	if truncate {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return nil, nil, err
		}
	}
	return f, info, nil
}

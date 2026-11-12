//go:build unix

package server

import (
	"errors"
	"os"
	"syscall"
)

// openRegularFile opens path without blocking on a FIFO and returns the handle
// with its Stat taken on the opened object. A non-regular object (FIFO, device,
// socket) is refused. With noFollow a link in the final component is refused
// too. O_TRUNC is applied only after the type check, so a refused object is
// never modified.
func openRegularFile(path string, flag int, perm os.FileMode, noFollow bool) (*os.File, os.FileInfo, error) {
	truncate := flag&os.O_TRUNC != 0
	flag = flag&^os.O_TRUNC | syscall.O_NONBLOCK | syscall.O_NOCTTY
	if noFollow {
		flag |= syscall.O_NOFOLLOW
	}
	f, err := os.OpenFile(path, flag, perm)
	if errors.Is(err, syscall.ENXIO) {
		// Opening a FIFO for writing with no reader, or a socket, fails this way.
		return nil, nil, errNotRegularFile
	}
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

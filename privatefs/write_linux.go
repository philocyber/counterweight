package privatefs

import (
	"os"

	"golang.org/x/sys/unix"
)

func openExistingPrivateWrite(path string) (*os.File, error) {
	parent, name, err := openLinuxParent(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	defer unix.Close(parent)
	// Inspect/protect the inode before acquiring writable access, retaining
	// owner-readable legacy 0444 files. Owner-write-only files use a write handle.
	flags := unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|flags, 0)
	if os.IsPermission(err) {
		fd, err = unix.Openat(parent, name, unix.O_WRONLY|flags, 0)
	}
	if os.IsPermission(err) {
		// Even mode-000 owner files can be repaired without using a mutable
		// pathname for chmod. O_PATH acquires metadata access without data access.
		fd, err = unix.Openat(parent, name, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	}
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	inspection := os.NewFile(uintptr(fd), path)
	defer inspection.Close()
	expected, err := inspection.Stat()
	if err != nil {
		return nil, err
	}
	if !expected.Mode().IsRegular() {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	if err := protectOpenedFile(inspection); err != nil {
		return nil, err
	}
	fd, err = unix.Openat(parent, name, unix.O_WRONLY|flags, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		f.Close()
		if err == nil {
			err = os.ErrInvalid
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return f, nil
}

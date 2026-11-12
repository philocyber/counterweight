package privatefs

import (
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func protectOpenedFile(f *os.File) error { return protectOpenedLinux(f, 0600) }

func protectOpenedLinux(f *os.File, mode os.FileMode) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm() == mode {
		// Already-private read-only mounts do not need chmod.
		return nil
	}
	err = f.Chmod(mode)
	if !errors.Is(err, unix.EBADF) {
		return err
	}
	// O_PATH pins metadata without requiring data permissions. fchmodat2 can
	// change that exact inode; older kernels need our live descriptor in procfs.
	err = unix.Fchmodat(int(f.Fd()), "", uint32(mode), unix.AT_EMPTY_PATH)
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) {
		return os.Chmod("/proc/self/fd/"+strconv.FormatUint(uint64(f.Fd()), 10), mode)
	}
	return err
}

func openPermissionObject(path string, directory bool) (*os.File, os.FileInfo, error) {
	fd, err := openLinuxPath(path, unix.O_PATH, 0)
	if err != nil {
		return nil, nil, &os.PathError{Op: "protect", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = os.ErrInvalid
		}
		return nil, nil, &os.PathError{Op: "protect", Path: path, Err: err}
	}
	return f, info, nil
}

func protect(path string, directory bool) error {
	f, _, err := openPermissionObject(path, directory)
	if err != nil {
		return err
	}
	defer f.Close()
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return protectOpenedLinux(f, mode)
}

func protectOwnerFile(path string, _ os.FileMode) error {
	f, info, err := openPermissionObject(path, false)
	if err != nil {
		return err
	}
	defer f.Close()
	// Derive owner bits from the opened object, not an earlier path lookup.
	return protectOpenedLinux(f, info.Mode().Perm()&0700)
}

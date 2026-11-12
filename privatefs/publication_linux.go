package privatefs

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/sys/unix"
)

func preparePrivatePublication(path string) (*privatePublication, error) {
	name, err := privatePublicationName()
	if err != nil {
		return nil, err
	}
	parent, destination, err := openLinuxParent(path)
	if err != nil {
		return nil, &os.PathError{Op: "publish-private", Path: path, Err: err}
	}
	// An unnamed inode avoids exposing a staging entry that can be replaced.
	fd, err := unix.Openat(parent, ".", unix.O_WRONLY|unix.O_TMPFILE|unix.O_CLOEXEC, 0600)
	named := false
	if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EISDIR) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		fd, err = unix.Openat(parent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
		named = err == nil
	}
	if err != nil {
		unix.Close(parent)
		return nil, &os.PathError{Op: "publish-private", Path: path, Err: err}
	}
	tmp := filepath.Join(filepath.Dir(path), name)
	f := os.NewFile(uintptr(fd), tmp)
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			// The named fallback still requires a caller-owned parent directory.
			if named {
				unix.Unlinkat(parent, name, 0)
			}
			f.Close()
			unix.Close(parent)
		})
	}
	if err := protectOpenedLinux(f, 0600); err != nil {
		cleanup()
		return nil, err
	}
	return &privatePublication{file: f, cleanup: cleanup, publish: func() error {
		// Keep the source descriptor live until linking. AT_EMPTY_PATH needs
		// a capability on some kernels; procfs provides the same pinned inode
		// for ordinary owners without reverting to a replaceable source name.
		err := unix.Linkat(fd, "", parent, destination, unix.AT_EMPTY_PATH)
		if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			err = unix.Linkat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), parent, destination, unix.AT_SYMLINK_FOLLOW)
		}
		if err != nil {
			return &os.LinkError{Op: "link", Old: tmp, New: path, Err: err}
		}
		return nil
	}}, nil
}

package privatefs

import (
	"os"

	"golang.org/x/sys/unix"
)

func snapshotPrivateRead(path string) (os.FileInfo, *os.File, error) {
	f, err := openPrivateRead(path)
	if err != nil {
		return nil, nil, err
	}
	// Keep the inspected inode alive through the read, even if it is unlinked.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return info, f, nil
}

func openPrivateRead(path string) (*os.File, error) {
	// O_NONBLOCK prevents a swapped FIFO from hanging before handle validation.
	fd, err := openLinuxPath(path, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "read", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

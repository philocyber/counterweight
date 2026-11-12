package privatefs

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Retain literal components: cleaning link/../file before traversal would hide
// an indirect ancestor. Every actual directory lookup is relative to a pinned
// descriptor, with O_DIRECTORY|O_NOFOLLOW enforcing its role atomically.
func linuxPrivateComponents(path string) ([]string, error) {
	if path == "" {
		return nil, os.ErrInvalid
	}
	abs := path
	if !filepath.IsAbs(abs) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		abs = cwd + "/" + path
	}
	if filepath.Clean(abs) == "/" {
		return nil, os.ErrInvalid // A volume root is not a private store.
	}
	return strings.Split(abs, "/"), nil
}

func openLinuxParent(path string) (int, string, error) {
	parts, err := linuxPrivateComponents(path)
	if err != nil {
		return -1, "", err
	}
	name := parts[len(parts)-1]
	if name == "" {
		name = "." // Preserve trailing-slash directory semantics.
	}
	flags := unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	parent, err := unix.Open("/", flags, 0)
	if err != nil {
		return -1, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." {
			continue
		}
		next, err := unix.Openat(parent, part, flags, 0)
		unix.Close(parent)
		if err != nil {
			return -1, "", err
		}
		parent = next
	}
	return parent, name, nil
}

func openOrCreateLinuxDirectory(parent int, name, path string) (*os.File, error) {
	flags := unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(parent, name, flags, 0)
	created := false
	if os.IsNotExist(err) {
		err = unix.Mkdirat(parent, name, 0700)
		created = err == nil
		if err == nil || os.IsExist(err) {
			fd, err = unix.Openat(parent, name, flags, 0)
		}
	}
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if created {
		// Restore owner access if a restrictive umask removed it. Existing
		// ancestors are never chmodded, and indirect entries cannot be opened.
		if err := protectOpenedLinux(f, 0700); err != nil {
			f.Close()
			return nil, err
		}
	}
	return f, nil
}

func makePrivateDirectory(path string) error {
	parts, err := linuxPrivateComponents(path)
	if err != nil {
		return &os.PathError{Op: "mkdir-private", Path: path, Err: err}
	}
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	parent := os.NewFile(uintptr(fd), "/")
	defer func() { parent.Close() }()
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		next, err := openOrCreateLinuxDirectory(int(parent.Fd()), part, path)
		if err != nil {
			return &os.PathError{Op: "mkdir-private", Path: path, Err: err}
		}
		parent.Close()
		parent = next
	}
	return protectOpenedLinux(parent, 0700)
}

func openLinuxPath(path string, flags int, mode uint32) (int, error) {
	parent, name, err := openLinuxParent(path)
	if err != nil {
		return -1, err
	}
	defer unix.Close(parent)
	return unix.Openat(parent, name, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
}

func createPrivate(path string) (*os.File, error) {
	fd, err := openLinuxPath(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	return os.NewFile(uintptr(fd), path), nil
}

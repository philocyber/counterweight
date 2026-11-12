package privatefs

import (
	"os"

	"golang.org/x/sys/windows"
)

func snapshotPrivateRead(path string) (os.FileInfo, *os.File, error) {
	f, err := openPrivateRead(path)
	if err != nil {
		return nil, nil, err
	}
	// A handle-based Stat captures file ID now. Path-based Lstat on Windows
	// resolves that ID lazily in SameFile, potentially after path replacement.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return info, f, nil
}

// openPrivateRead keeps the native file object pinned while permitting a concurrent
// startup to retire its obsolete directory entry. Go's normal Windows read
// handle omits delete sharing, which can otherwise make duplicate cleanup fail.
func openPrivateRead(path string) (*os.File, error) {
	h, err := openWindowsPath(path, "read", windows.GENERIC_READ, privatePathSharing, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return nil, &os.PathError{Op: "read", Path: path, Err: err}
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, &os.PathError{Op: "read", Path: path, Err: os.ErrInvalid}
	}
	return os.NewFile(uintptr(h), path), nil
}

func createPrivate(path string) (*os.File, error) {
	sd, err := privateDescriptor(false)
	if err != nil {
		return nil, err
	}
	h, err := openWindowsPath(path, "create", windows.GENERIC_WRITE, 0, windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE, sd)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

func openExistingPrivateWrite(path string) (*os.File, error) {
	// OPEN_EXISTING must not truncate: validation and DACL repair happen first.
	h, err := openWindowsPath(path, "open", windows.GENERIC_WRITE|windows.READ_CONTROL|windows.WRITE_DAC, privatePathSharing, windows.FILE_OPEN, windows.FILE_NON_DIRECTORY_FILE, nil)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		windows.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	return os.NewFile(uintptr(h), path), nil
}

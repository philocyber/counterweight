package privatefs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Use the basic information class, whose value is 11. The x/sys constant
// named FileLinkInformation currently denotes the extended class (72).
const privateFileLinkInformation = 11

type privateWindowsLinkInformation struct {
	replaceIfExists uint8
	rootDirectory   windows.Handle
	fileNameLength  uint32
	fileName        [260]uint16
}

func preparePrivatePublication(path string) (*privatePublication, error) {
	name, err := privatePublicationName()
	if err != nil {
		return nil, err
	}
	parent, destination, err := openWindowsParent(path)
	if err != nil {
		return nil, err
	}
	if !filepath.IsLocal(destination) || strings.ContainsAny(destination, `/\`) || strings.HasSuffix(destination, ".") || strings.HasSuffix(destination, " ") {
		windows.CloseHandle(parent)
		return nil, &os.PathError{Op: "publish-private", Path: path, Err: os.ErrInvalid}
	}
	destinationUTF16, err := windows.UTF16FromString(destination)
	if err != nil || len(destinationUTF16) > 260 {
		windows.CloseHandle(parent)
		if err == nil {
			err = os.ErrInvalid
		}
		return nil, &os.PathError{Op: "publish-private", Path: path, Err: err}
	}
	sd, err := privateDescriptor(false)
	if err != nil {
		windows.CloseHandle(parent)
		return nil, err
	}
	handle, err := openRelativeWindows(parent, name, windows.GENERIC_WRITE|windows.DELETE, privatePathSharing,
		windows.FILE_CREATE, windows.FILE_NON_DIRECTORY_FILE, sd)
	if err != nil {
		windows.CloseHandle(parent)
		return nil, &os.PathError{Op: "publish-private", Path: path, Err: err}
	}
	tmp := filepath.Join(filepath.Dir(path), name)
	f := os.NewFile(uintptr(handle), tmp)
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			// Delete the opened staging link, even if its original pathname was
			// replaced. A separately published hard link remains intact.
			remove := byte(1)
			var status windows.IO_STATUS_BLOCK
			windows.NtSetInformationFile(handle, &status, &remove, 1, windows.FileDispositionInformation)
			f.Close()
			windows.CloseHandle(parent)
		})
	}
	return &privatePublication{file: f, cleanup: cleanup, publish: func() error {
		info := privateWindowsLinkInformation{rootDirectory: parent, fileNameLength: uint32((len(destinationUTF16) - 1) * 2)}
		copy(info.fileName[:], destinationUTF16)
		var status windows.IO_STATUS_BLOCK
		err := windows.NtSetInformationFile(handle, &status, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), privateFileLinkInformation)
		if native, ok := err.(windows.NTStatus); ok {
			err = native.Errno()
		}
		if err != nil {
			return &os.LinkError{Op: "link", Old: tmp, New: path, Err: err}
		}
		return nil
	}}, nil
}

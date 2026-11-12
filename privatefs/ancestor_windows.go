package privatefs

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const privatePathSharing = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE

// Open one component against a pinned directory object. Never ask Win32 to
// resolve a complete path again after the ancestor chain has been validated.
func openRelativeWindows(parent windows.Handle, name string, access, share, disposition, options uint32, sd *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	if !filepath.IsLocal(name) || strings.ContainsAny(name, `/\`) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return 0, os.ErrInvalid
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		RootDirectory: parent, ObjectName: objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: sd,
	}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE|windows.FILE_READ_ATTRIBUTES, &attributes, &status, nil,
		windows.FILE_ATTRIBUTE_NORMAL, share, disposition,
		options|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	if native, ok := err.(windows.NTStatus); ok {
		err = native.Errno() // Keep os.IsExist/os.IsNotExist retry semantics.
	}
	return handle, err
}

func validateWindowsParent(handle windows.Handle) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return os.ErrInvalid
	}
	return nil
}

func windowsPrivateComponents(path string) (string, []string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	volume := filepath.VolumeName(abs)
	root := volume + `\`
	if volume == "" || !strings.HasPrefix(abs, root) || abs == root {
		return "", nil, os.ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(abs, root), `\`)
	return root, parts, nil
}

func openWindowsDirectoryChain(root string, parts []string, create bool) (windows.Handle, error) {
	rootName, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, err
	}
	access := uint32(windows.FILE_READ_ATTRIBUTES | windows.FILE_TRAVERSE | windows.SYNCHRONIZE)
	parent, err := windows.CreateFile(rootName, access, privatePathSharing, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	if err := validateWindowsParent(parent); err != nil {
		windows.CloseHandle(parent)
		return 0, err
	}
	var sd *windows.SECURITY_DESCRIPTOR
	disposition := uint32(windows.FILE_OPEN)
	if create {
		sd, err = privateDescriptor(true)
		if err != nil {
			windows.CloseHandle(parent)
			return 0, err
		}
		disposition = windows.FILE_OPEN_IF
	}
	for index, part := range parts {
		childAccess := access
		if create && index == len(parts)-1 {
			childAccess = windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL | windows.WRITE_DAC | windows.SYNCHRONIZE
		}
		// FILE_OPEN_IF applies this descriptor only to newly created directories.
		// Existing ancestor ACLs are not repaired or replaced.
		next, err := openRelativeWindows(parent, part, childAccess, privatePathSharing, disposition, windows.FILE_DIRECTORY_FILE, sd)
		windows.CloseHandle(parent)
		if err != nil {
			return 0, err
		}
		parent = next
		if err := validateWindowsParent(parent); err != nil {
			windows.CloseHandle(parent)
			return 0, err
		}
	}
	return parent, nil
}

func openWindowsParent(path string) (windows.Handle, string, error) {
	root, parts, err := windowsPrivateComponents(path)
	if err != nil {
		return 0, "", err
	}
	parent, err := openWindowsDirectoryChain(root, parts[:len(parts)-1], false)
	return parent, parts[len(parts)-1], err
}

func makePrivateDirectory(path string) error {
	root, parts, err := windowsPrivateComponents(path)
	if err != nil {
		return &os.PathError{Op: "mkdir-private", Path: path, Err: err}
	}
	handle, err := openWindowsDirectoryChain(root, parts, true)
	if err != nil {
		return &os.PathError{Op: "mkdir-private", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(handle), path)
	defer f.Close()
	return protectOpenedWindows(f, true)
}

func openWindowsPath(path, operation string, access, share, disposition, options uint32, sd *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	parent, name, err := openWindowsParent(path)
	if err != nil {
		return 0, &os.PathError{Op: operation, Path: path, Err: err}
	}
	defer windows.CloseHandle(parent)
	handle, err := openRelativeWindows(parent, name, access, share, disposition, options, sd)
	if err != nil {
		return 0, &os.PathError{Op: operation, Path: path, Err: err}
	}
	return handle, nil
}

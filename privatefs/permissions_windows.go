package privatefs

import (
	"golang.org/x/sys/windows"
	"os"
)

func protectOwnerFile(path string, mode os.FileMode) error { return protect(path, false) }

func protectOpenedFile(f *os.File) error {
	return protectOpenedWindows(f, false)
}

func protectOpenedWindows(f *os.File, directory bool) error {
	sd, err := privateDescriptor(directory)
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}

func privateDescriptor(directory bool) (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	flags := ""
	if directory {
		flags = "OICI"
	}
	// Preserve privileged OS/service recovery while removing inherited/explicit
	// access for unrelated users. Children inherit only these three principals.
	sddl := "D:P(A;" + flags + ";FA;;;SY)(A;" + flags + ";FA;;;BA)(A;" + flags + ";FA;;;" + user.User.Sid.String() + ")"
	return windows.SecurityDescriptorFromString(sddl)
}

func openPermissionObject(path string, directory bool) (*os.File, os.FileInfo, error) {
	options := uint32(windows.FILE_NON_DIRECTORY_FILE)
	if directory {
		options = windows.FILE_DIRECTORY_FILE
	}
	h, err := openWindowsPath(path, "protect", windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.WRITE_DAC, privatePathSharing, windows.FILE_OPEN, options, nil)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		f.Close()
		return nil, nil, err
	}
	isDirectory := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || isDirectory != directory {
		f.Close()
		return nil, nil, &os.PathError{Op: "protect", Path: path, Err: os.ErrInvalid}
	}
	stat, err := f.Stat()
	if err != nil || directory && !stat.IsDir() || !directory && !stat.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = os.ErrInvalid
		}
		return nil, nil, &os.PathError{Op: "protect", Path: path, Err: err}
	}
	return f, stat, nil
}

func protect(path string, directory bool) error {
	f, _, err := openPermissionObject(path, directory)
	if err != nil {
		return err
	}
	defer f.Close()
	return protectOpenedWindows(f, directory)
}

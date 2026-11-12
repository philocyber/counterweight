package evidence

import (
	"os"

	"golang.org/x/sys/windows"
)

func publishBody(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	// The caller has flushed/closed the body. Request native write-through
	// publication instead of attempting POSIX directory fsync on Windows.
	// Do not allow cross-volume copy, which could replace the private ACL.
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "publish", Old: source, New: destination, Err: err}
	}
	return nil
}

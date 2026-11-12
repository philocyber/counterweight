package server

import (
	"errors"
	"golang.org/x/sys/windows"
)

func temporaryJWTFileError(err error) bool {
	// Delete-pending files can report access denied until other startup handles
	// close. Persistent access errors still fail after the bounded retry window.
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

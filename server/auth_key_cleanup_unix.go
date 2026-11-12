//go:build !windows

package server

func temporaryJWTFileError(err error) bool { return false }

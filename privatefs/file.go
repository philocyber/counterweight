package privatefs

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

// Create opens a new, exclusively created file that is private before writing.
func Create(path string) (*os.File, error) { return createPrivate(path) }

// ReadFile rejects indirect/nonregular entries and verifies the opened file's
// identity before reading. The handle stays pinned while a concurrent startup
// retires its directory entry. Ancestor ownership is still the caller's duty.
func ReadFile(path string) ([]byte, error) {
	info, identityHandle, err := snapshotPrivateRead(path)
	if err != nil {
		return nil, err
	}
	if identityHandle != nil {
		defer identityHandle.Close()
	}
	if !info.Mode().IsRegular() {
		return nil, locale.Errorf("Private storage contains unsupported file type: %s", path)
	}
	f, err := openPrivateRead(path)
	if err != nil {
		return nil, err
	}
	data, readErr := readOpenedPrivateFile(f, info)
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	return data, closeErr
}

func readOpenedPrivateFile(f *os.File, expected os.FileInfo) ([]byte, error) {
	actual, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(expected, actual) {
		return nil, locale.Errorf("Private storage contains unsupported file type: %s", f.Name())
	}
	return io.ReadAll(f)
}

// CreateTemp creates a private write-only temporary file. Like os.CreateTemp,
// the last '*' in pattern is replaced; a pattern without '*' gains a suffix.
func CreateTemp(dir, pattern string) (*os.File, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if strings.ContainsAny(pattern, `/\`) {
		return nil, &os.PathError{Op: "createtemp", Path: pattern, Err: os.ErrInvalid}
	}
	prefix, suffix := pattern, ""
	if i := strings.LastIndexByte(pattern, '*'); i >= 0 {
		prefix, suffix = pattern[:i], pattern[i+1:]
	}
	for i := 0; i < 10; i++ {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, err
		}
		path := filepath.Join(dir, prefix+hex.EncodeToString(random[:])+suffix)
		file, err := createPrivate(path)
		if os.IsExist(err) {
			continue
		}
		return file, err
	}
	return nil, &os.PathError{Op: "createtemp", Path: dir, Err: os.ErrExist}
}

// CreateTruncate protects an existing regular file before truncation, or creates
// a new private file exclusively. Callers must own the destination directory;
// this does not sandbox hostile concurrent changes to its path components.
func CreateTruncate(path string) (*os.File, error) {
	f, err := createPrivate(path)
	if !os.IsExist(err) {
		return f, err
	}
	f, err = openExistingPrivateWrite(path)
	if err != nil {
		return nil, err
	}
	if err := protectAndTruncateOpenedFile(f); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func protectAndTruncateOpenedFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return locale.Errorf("Private storage contains unsupported file type: %s", f.Name())
	}
	if err := protectOpenedFile(f); err != nil {
		return err
	}
	return f.Truncate(0)
}

// ProtectFile tightens one regular file without changing its parent directory.
func ProtectFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return locale.Errorf("Private storage contains unsupported file type: %s", path)
	}
	return protect(path, false)
}

// PublishFile publishes complete private bytes without replacing an existing
// destination. The temporary file is private from creation, before any write.
// Filesystems without hard-link support fail closed rather than fall back to a
// non-atomic copy or an overwrite of another process's signing key.
func PublishFile(path string, data []byte) error {
	publication, err := preparePrivatePublication(path)
	if err != nil {
		return err
	}
	defer publication.cleanup()
	if _, err := publication.file.Write(data); err != nil {
		return err
	}
	if err := publication.file.Sync(); err != nil {
		return err
	}
	return publication.publish()
}

type privatePublication struct {
	file    *os.File
	publish func() error
	cleanup func()
}

// PublicationTempPrefix starts the name of the temporary file PublishFile
// creates next to its destination before linking it into place.
const PublicationTempPrefix = ".private-"

func privatePublicationName() (string, error) {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return PublicationTempPrefix + hex.EncodeToString(suffix[:]), nil
}

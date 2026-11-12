// Package privatefs protects application-owned secret storage without changing
// permissions on its ancestors or following links into unrelated locations.
package privatefs

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Philocyber/counterweight/locale"
)

func ProtectTree(root string) error {
	if err := makePrivateDirectory(root); err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return locale.Errorf("Private storage must not contain symbolic links: %s", path)
		}
		if !entry.IsDir() && !entry.Type().IsRegular() {
			return locale.Errorf("Private storage contains unsupported file type: %s", path)
		}
		return protect(path, entry.IsDir())
	})
}

// ProtectDirectory protects only this application-owned directory, not its
// ancestors or existing children. New Windows children inherit its private ACL.
func ProtectDirectory(path string) error {
	return makePrivateDirectory(path)
}

// ProtectTreePreservingOwner protects an owned archive/staging tree while
// retaining each regular file's owner execute/read-only permissions. Links and
// other nonregular entries are skipped by permission repair, never followed.
// The root itself must be a real directory.
func ProtectTreePreservingOwner(root string) error {
	if err := ProtectDirectory(root); err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return protect(path, true)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return ProtectFilePreservingOwner(path)
	})
}

func ProtectFilePreservingOwner(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return locale.Errorf("Private storage contains unsupported file type: %s", path)
	}
	return protectOwnerFile(path, info.Mode().Perm()&0700)
}

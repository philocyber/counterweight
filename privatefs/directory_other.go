//go:build !windows && !linux

package privatefs

import (
	"os"

	"github.com/Philocyber/counterweight/locale"
)

func makePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return locale.Errorf("Private storage contains unsupported file type: %s", path)
	}
	return protect(path, true)
}

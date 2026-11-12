//go:build !linux && !windows

package privatefs

import (
	"os"
	"path/filepath"
)

func preparePrivatePublication(path string) (*privatePublication, error) {
	name, err := privatePublicationName()
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(filepath.Dir(path), name)
	f, err := createPrivate(tmp)
	if err != nil {
		return nil, err
	}
	return &privatePublication{file: f, publish: func() error {
		if err := f.Close(); err != nil {
			return err
		}
		return os.Link(tmp, path)
	}, cleanup: func() { f.Close(); os.Remove(tmp) }}, nil
}

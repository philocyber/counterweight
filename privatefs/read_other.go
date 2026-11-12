//go:build !windows && !linux

package privatefs

import "os"

func snapshotPrivateRead(path string) (os.FileInfo, *os.File, error) {
	info, err := os.Lstat(path)
	return info, nil, err
}

// The shared reader checks entry/object identity before reading. These platforms
// retain their ordinary open behavior; native no-follow flags are not asserted.
func openPrivateRead(path string) (*os.File, error) { return os.Open(path) }

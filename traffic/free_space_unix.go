//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package traffic

import "golang.org/x/sys/unix"

func trafficAvailableBytes(dir string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	if stat.Bavail <= 0 || stat.Bsize <= 0 {
		return 0, nil
	}
	blocks, size := uint64(stat.Bavail), uint64(stat.Bsize)
	if blocks > ^uint64(0)/size {
		return ^uint64(0), nil
	}
	return blocks * size, nil
}

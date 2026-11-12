//go:build !windows && !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package traffic

import "github.com/Philocyber/counterweight/locale"

func trafficAvailableBytes(string) (uint64, error) {
	return 0, locale.NewError("Traffic free-space accounting unavailable on this platform")
}

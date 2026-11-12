package traffic

import (
	"io"
	"os"
	"strconv"

	"github.com/Philocyber/counterweight/locale"
)

const defaultFreeReserve int64 = 256 << 20

var ErrFreeReserve = locale.NewError("Traffic write would consume reserved free space")

func freeReserveFromEnv() (int64, error) {
	reserve := defaultFreeReserve
	if raw := os.Getenv("COUNTERWEIGHT_TRAFFIC_FREE_BYTES"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return 0, locale.NewError("COUNTERWEIGHT_TRAFFIC_FREE_BYTES must be a positive byte count")
		}
		reserve = value
	}
	return reserve, nil
}

// Leave allocation rounding/metadata headroom at each growth boundary. This
// is a check against current volume availability, not a reservation against
// writes performed by unrelated processes on the same volume.
func (t *Traffic) checkFreeGrowth(additional int64) error {
	const allocationHeadroom = 64 << 10
	if additional < 0 || additional > int64(1<<63-1)-allocationHeadroom {
		return ErrFreeReserve
	}
	return t.checkFreeSpace(additional + allocationHeadroom)
}

type reserveWriter struct {
	traffic *Traffic
	writer  io.Writer
}

func (w reserveWriter) Write(body []byte) (int, error) {
	w.traffic.walVFS.mu.Lock()
	defer w.traffic.walVFS.mu.Unlock()
	if err := w.traffic.checkFreeGrowth(int64(len(body))); err != nil {
		return 0, err
	}
	n, err := w.writer.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	return n, err
}

func (t *Traffic) checkFreeSpace(additional int64) error {
	if additional < 0 {
		return ErrFreeReserve
	}
	if additional == 0 {
		additional = 4096
	} // Even an empty new blob allocates filesystem metadata.
	available, err := trafficAvailableBytes(t.dir)
	if err != nil {
		return err
	}
	reserve := uint64(t.freeReserve)
	if available < reserve || uint64(additional) > available-reserve {
		return locale.Errorf("%w (reserve=%d bytes)", ErrFreeReserve, t.freeReserve)
	}
	return nil
}

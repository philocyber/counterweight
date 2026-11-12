package traffic

import (
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

const defaultExchangeBytes int64 = 64 << 20
const defaultHostBytes int64 = 256 << 20
const defaultMaxExchanges int64 = 100000

var ErrCaptureBudget = locale.NewError("Traffic capture exceeds exchange or host limit")
var ErrCaptureRetention = locale.NewError("Traffic retained exchange limit reached")

func captureRetentionFromEnv() (int64, error) {
	if raw := os.Getenv("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return 0, locale.NewError("COUNTERWEIGHT_TRAFFIC_MAX_EXCHANGES must be a positive exchange count")
		}
		return value, nil
	}
	return defaultMaxExchanges, nil
}

func captureLimitFromEnv(name string, fallback int64) (int64, error) {
	if raw := os.Getenv(name); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return 0, locale.Errorf("%s must be a positive byte count", name)
		}
		return value, nil
	}
	return fallback, nil
}

// Logical bytes charge repeated bodies even when their physical blob is shared.
// The fixed row charge also bounds a stream of empty exchanges. Physical index,
// WAL and blob allocation remain governed by their separate storage budgets.
func captureBytes(req, resp []byte, fields ...string) int64 {
	total := int64(128)
	add := func(n int) {
		if int64(n) > math.MaxInt64-total {
			total = math.MaxInt64
		} else {
			total += int64(n)
		}
	}
	add(len(req))
	add(len(resp))
	for _, field := range fields {
		add(len(field))
	}
	return total
}

// Called under wmu before spilling bodies. Query retained rows rather than an
// in-memory counter so restarts, archive restores and deletion share accounting.
// Hostnames share an allowance across ports, ASCII case and trailing dots.
func (t *Traffic) checkCaptureAdmission(host string, additional int64) error {
	if additional < 0 || additional > t.exchangeLimit || additional > t.hostLimit {
		return ErrCaptureBudget
	}
	var rows int64
	if err := t.db.QueryRow(`SELECT count(*) FROM exchanges`).Scan(&rows); err != nil {
		return err
	}
	if rows < 0 || rows >= t.maxExchanges {
		return ErrCaptureRetention
	}
	var retained int64
	err := t.db.QueryRow(`SELECT COALESCE(SUM(
128 + max(0,coalesce(e.req_len,0),coalesce(length(b.req_body),0))
 + max(0,coalesce(e.resp_len,0),coalesce(length(b.resp_body),0))
 + length(CAST(coalesce(e.host,'') AS BLOB))
 + length(CAST(coalesce(e.method,'') AS BLOB))
 + length(CAST(coalesce(e.url_template,'') AS BLOB))
 + length(CAST(coalesce(e.url,'') AS BLOB))
 + length(CAST(coalesce(e.content_type,'') AS BLOB))
 + length(CAST(coalesce(b.req_head,'') AS BLOB))
 + length(CAST(coalesce(b.resp_head,'') AS BLOB))),0)
FROM exchanges e LEFT JOIN exchange_bodies b ON b.id=e.id
WHERE lower(rtrim(e.host,'.'))=?`, strings.ToLower(strings.TrimRight(host, "."))).Scan(&retained)
	// SQLite integer overflow, malformed legacy metadata and accounting errors
	// fail admission rather than allowing the retained total to wrap around.
	if err != nil {
		return err
	}
	if retained < 0 || retained > t.hostLimit-additional {
		return ErrCaptureBudget
	}
	return nil
}

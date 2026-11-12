package traffic

import (
	"context"
	"database/sql/driver"
	"fmt"
	"os"
	"strconv"

	"github.com/Philocyber/counterweight/locale"
)

const defaultIndexBudget int64 = 1 << 30

var ErrIndexBudget = locale.NewError("Traffic index storage limit reached")

func indexBudgetFromEnv() (int64, error) {
	limit := defaultIndexBudget
	if raw := os.Getenv("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES"); raw != "" {
		var err error
		limit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || limit < 256*1024 {
			return 0, locale.NewError("COUNTERWEIGHT_TRAFFIC_INDEX_BYTES must be at least 262144 bytes")
		}
	}
	return limit, nil
}

// A local connector applies limits to every pooled connection, including the
// bare-filename DSN fallback. It does not change other SQLite databases/drivers.
type indexConnector struct {
	base  driver.Driver
	dsn   string
	limit int64
	vfs   *boundedWALVFS
}

func (c *indexConnector) Driver() driver.Driver { return c.base }
func (c *indexConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if c.vfs != nil {
		if err := c.vfs.retainConnection(); err != nil {
			return nil, err
		}
	}
	retained := c.vfs != nil
	defer func() {
		if retained {
			_ = c.vfs.releaseConnection()
		}
	}()
	conn, err := c.base.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	exec, hasExec := conn.(driver.ExecerContext)
	query, hasQuery := conn.(driver.QueryerContext)
	if !hasExec || !hasQuery {
		return nil, fmt.Errorf("SQLite driver lacks contextual pragma interfaces")
	}
	if _, err := exec.ExecContext(ctx, "PRAGMA busy_timeout=5000", nil); err != nil {
		return nil, err
	}
	rows, err := query.QueryContext(ctx, "PRAGMA page_size", nil)
	if err != nil {
		return nil, err
	}
	values := make([]driver.Value, 1)
	err = rows.Next(values)
	rows.Close()
	if err != nil {
		return nil, err
	}
	pageSize, valid := values[0].(int64)
	if !valid || pageSize <= 0 {
		return nil, fmt.Errorf("invalid SQLite page size")
	}
	pages := c.limit / pageSize
	if pages > 4294967294 {
		pages = 4294967294
	}
	if _, err := exec.ExecContext(ctx, fmt.Sprintf("PRAGMA max_page_count=%d", pages), nil); err != nil {
		return nil, err
	}
	if c.vfs != nil {
		native, supported := conn.(nativeIndexConnection)
		if !supported {
			return nil, fmt.Errorf("SQLite driver lacks required traffic connection interfaces")
		}
		conn = &boundedIndexConnection{nativeIndexConnection: native, vfs: c.vfs}
		retained = false
	}
	ok = true
	return conn, nil
}

// Reserve maintenance headroom. This admission estimate is deliberately
// conservative; max_page_count is the independent SQLite allocation ceiling.
func (t *Traffic) checkIndexAdmission(additional int64) error {
	if t.recoveryPaused.Load() {
		return locale.NewError("Traffic capture paused until WAL recovery succeeds")
	}
	if additional == 0 {
		return nil
	}
	if err := t.checkFreeSpace(additional); err != nil {
		return err
	}
	// The index estimate includes 128 KiB of database maintenance headroom.
	// WAL retains this allowance, reduced to half the configured threshold only
	// for small limits so the supported 64 KiB minimum can still capture data.
	walEstimate := additional
	if walEstimate >= 128<<10 {
		headroom := int64(128 << 10)
		if headroom > t.walAdmission/2 {
			headroom = t.walAdmission / 2
		}
		walEstimate = walEstimate - (128 << 10) + headroom
	}
	if err := t.checkWALAdmission(walEstimate); err != nil {
		return err
	}
	var pages, size, free int64
	if err := t.db.QueryRow(`SELECT page_count,page_size,freelist_count FROM pragma_page_count,pragma_page_size,pragma_freelist_count`).Scan(&pages, &size, &free); err != nil {
		return err
	}
	reserve := t.indexLimit / 10
	if reserve > 64<<20 {
		reserve = 64 << 20
	}
	if reserve < 128<<10 {
		reserve = 128 << 10
	}
	available := t.indexLimit - reserve - (pages-free)*size
	if additional > available {
		return locale.Errorf("%w (limit=%d bytes)", ErrIndexBudget, t.indexLimit)
	}
	return nil
}

func indexWriteEstimate(url, reqHead, respHead string, reqBody, respBody []byte, reqType, respType string) int64 {
	size := int64(len(url)) + int64(len(reqHead)) + int64(len(respHead))
	for n, body := range [][]byte{reqBody, respBody} {
		inline := len(body)
		if inline > maxInlineBody {
			inline = blobPreview
		}
		size += int64(inline)
		contentType := reqType
		if n == 1 {
			contentType = respType
		}
		if !isBinaryBody(contentType, body) {
			length := len(body)
			if length > maxIndexBody {
				length = maxIndexBody
			}
			size += int64(length)
		}
	}
	return size*16 + 128*1024
}

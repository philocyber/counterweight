package traffic

import (
	"database/sql/driver"
	"errors"
	"sync"

	sqlite "modernc.org/sqlite"
)

// Preserve the pinned driver's database/sql and file-control interfaces while
// owning its VFS registration through the entire native connection lifetime.
type nativeIndexConnection interface {
	driver.Conn
	driver.ConnPrepareContext
	driver.ConnBeginTx
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
	sqlite.FileControl
}

type boundedIndexConnection struct {
	nativeIndexConnection
	vfs        *boundedWALVFS
	once       sync.Once
	closeError error
}

func (c *boundedIndexConnection) Close() error {
	c.once.Do(func() {
		c.closeError = errors.Join(c.nativeIndexConnection.Close(), c.vfs.releaseConnection())
	})
	return c.closeError
}

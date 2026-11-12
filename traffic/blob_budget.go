package traffic

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"

	"github.com/Philocyber/counterweight/locale"
)

const defaultBlobBudget int64 = 1 << 30

var ErrBlobBudget = locale.NewError("Traffic blob storage limit reached")

type blobBudget struct {
	limit, used int64 // guarded by Traffic.wmu after Open
	dropped     atomic.Int64
}

type BlobStorageStatus struct {
	LimitBytes      int64 `json:"limit_bytes"`
	UsedBytes       int64 `json:"used_bytes"`
	DroppedCaptures int64 `json:"dropped_captures"`
}

func (t *Traffic) BlobStorageStatus() BlobStorageStatus {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	return BlobStorageStatus{t.blobs.limit, t.blobs.used, t.blobs.dropped.Load()}
}

func (t *Traffic) initBlobBudget() error {
	t.blobs.limit = defaultBlobBudget
	if raw := os.Getenv("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES"); raw != "" {
		limit, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || limit <= 0 {
			return locale.NewError("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES must be a positive byte count")
		}
		t.blobs.limit = limit
	}
	root := filepath.Join(t.dir, "_blobs", "sha256")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported entry in traffic blob store")
		}
		t.blobs.used += info.Size()
		return nil
	})
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Callers hold wmu across admission, atomic file publication and SQLite commit.
// Existing blobs consume no additional capacity; old evidence is never evicted.
func (t *Traffic) writeBlob(hash string, body []byte) (bool, error) {
	path := filepath.Join(t.dir, "_blobs", "sha256", hash[:2], hash+".bin")
	if info, err := os.Stat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() != int64(len(body)) {
			return false, errors.New("traffic blob content size mismatch")
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	size := int64(len(body))
	if err := t.checkFreeSpace(size); err != nil {
		return false, err
	}
	if t.blobs.used > t.blobs.limit || size > t.blobs.limit-t.blobs.used {
		return false, locale.Errorf("%w (limit=%d, used=%d bytes)", ErrBlobBudget, t.blobs.limit, t.blobs.used)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return false, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".blob-*")
	if err != nil {
		return false, err
	}
	defer func() { file.Close(); os.Remove(file.Name()) }()
	writer := reserveWriter{traffic: t, writer: file}
	for len(body) > 0 {
		n := len(body)
		if n > 64<<10 {
			n = 64 << 10
		}
		if _, err = writer.Write(body[:n]); err != nil {
			break
		}
		body = body[n:]
	}
	if err = errors.Join(err, file.Sync(), file.Close()); err != nil {
		return false, err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return false, err
	}
	t.blobs.used += size
	return true, nil
}

func (t *Traffic) removeBudgetedBlob(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	t.blobs.used -= info.Size()
	if t.blobs.used < 0 {
		t.blobs.used = 0
	}
	return nil
}

func (t *Traffic) undoNewBlobs(bodies ...storedBody) {
	for _, body := range bodies {
		if body.newBlob {
			var references int
			if err := t.db.QueryRow(`SELECT count(*) FROM blob_refs WHERE hash=?`, body.hash).Scan(&references); err != nil || references != 0 {
				continue
			}
			_ = t.removeBudgetedBlob(filepath.Join(t.dir, "_blobs", "sha256", body.hash[:2], body.hash+".bin"))
		}
	}
}

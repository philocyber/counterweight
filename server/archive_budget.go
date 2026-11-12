package server

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/Philocyber/counterweight/locale"
)

const defaultTaskArchiveBytes int64 = 4 << 30

var errTaskArchiveBudget = locale.NewError("Task archive storage limit reached")

func taskArchiveByteLimit() (int64, error) {
	limit := defaultTaskArchiveBytes
	if raw := os.Getenv("COUNTERWEIGHT_TASK_ARCHIVE_BYTES"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 || value > maxArchiveBytes {
			return 0, locale.NewError("COUNTERWEIGHT_TASK_ARCHIVE_BYTES must be positive and at most 140737488355328 bytes")
		}
		limit = value
	}
	return limit, nil
}

// Count only regular payload bytes without following symlinks. Files moved
// into staging already consume capacity even though moving adds no copy.
func archivePayloadBytes(root string, limit int64, excludeManifest bool) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || (excludeManifest && path == filepath.Join(root, "manifest.json")) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if info.Size() < 0 || info.Size() > limit-total {
			return taskArchiveBudgetError(limit)
		}
		total += info.Size()
		return nil
	})
	return total, err
}

type archiveStageBudget struct {
	mu          sync.Mutex
	limit, used int64
}

func (b *archiveStageBudget) wrap(destination io.Writer) io.Writer {
	return &archiveStageWriter{budget: b, destination: destination}
}

type archiveStageWriter struct {
	budget      *archiveStageBudget
	destination io.Writer
}

func (w *archiveStageWriter) Write(p []byte) (int, error) {
	b := w.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if int64(len(p)) > b.limit-b.used {
		return 0, taskArchiveBudgetError(b.limit)
	}
	n, err := w.destination.Write(p)
	b.used += int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

func taskArchiveBudgetError(limit int64) error {
	return locale.Errorf("%w (limit=%d bytes)", errTaskArchiveBudget, limit)
}

// Refuse an oversized write before delegation. A partial downstream write still
// consumes its actual bytes; callers retain normal close/rollback cleanup.
type archiveByteWriter struct {
	destination      io.Writer
	remaining, limit int64
}

func newArchiveByteWriter(destination io.Writer, limit int64) *archiveByteWriter {
	return &archiveByteWriter{destination: destination, remaining: limit, limit: limit}
}

func (w *archiveByteWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, taskArchiveBudgetError(w.limit)
	}
	n, err := w.destination.Write(p)
	w.remaining -= int64(n)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}

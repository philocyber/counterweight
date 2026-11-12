package traffic

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/privatefs"
)

// The caller holds wmu through publication and the archive's SQLite commit.
// Admission precedes body reads, and one opened source supplies both size and
// checksum. A source that grows cannot write past its admitted size.
func (t *Traffic) importArchiveBlob(hash, source string) (bool, error) {
	input, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("unsupported archived traffic blob")
	}
	size := info.Size()
	destination := filepath.Join(t.dir, "_blobs", "sha256", hash[:2], hash+".bin")
	if existing, err := os.Stat(destination); err == nil {
		if !existing.Mode().IsRegular() || existing.Size() != size {
			return false, errors.New("traffic blob content size mismatch")
		}
		// Deduplicated retries still verify source authenticity but need no
		// additional storage, even if a later operator limit is smaller.
		return false, copyVerifiedArchiveBlob(io.Discard, input, size, hash)
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := t.checkFreeSpace(size); err != nil {
		return false, err
	}
	if t.blobs.used > t.blobs.limit || size > t.blobs.limit-t.blobs.used {
		return false, locale.Errorf("%w (limit=%d, used=%d bytes)", ErrBlobBudget, t.blobs.limit, t.blobs.used)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return false, err
	}
	output, err := privatefs.CreateTemp(filepath.Dir(destination), ".blob-*")
	if err != nil {
		return false, err
	}
	defer func() { _ = output.Close(); _ = os.Remove(output.Name()) }()
	if err := copyVerifiedArchiveBlob(reserveWriter{traffic: t, writer: output}, input, size, hash); err != nil {
		return false, err
	}
	if err := errors.Join(output.Sync(), output.Close()); err != nil {
		return false, err
	}
	if err := os.Rename(output.Name(), destination); err != nil {
		return false, err
	}
	t.blobs.used += size
	return true, nil
}

func copyVerifiedArchiveBlob(destination io.Writer, source io.Reader, size int64, expected string) error {
	digest := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(destination, digest), source, size); err != nil {
		return err
	}
	var extra [1]byte
	if _, err := io.ReadFull(source, extra[:]); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("archived traffic blob size changed during import")
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected {
		return locale.Errorf("traffic blob checksum mismatch: %s", expected)
	}
	return nil
}

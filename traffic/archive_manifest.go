package traffic

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"

	"github.com/Philocyber/counterweight/locale"
)

func readArchiveSnapshot(path string, limit int64) (ArchiveSnapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return ArchiveSnapshot{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ArchiveSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return ArchiveSnapshot{}, errors.New("unsupported traffic archive manifest")
	}
	if info.Size() > limit {
		return ArchiveSnapshot{}, archiveManifestLimitError(limit)
	}
	return decodeArchiveSnapshot(file, limit)
}

// The configured index ceiling also bounds imported manifest bytes. The reader
// enforces it independently of Stat, so growth cannot enlarge the decoder's
// input. One probe byte distinguishes an exact-size file from an oversized one
// without delivering that byte to the JSON decoder or overflowing limit+1.
type archiveManifestReader struct {
	source           io.Reader
	remaining, limit int64
	terminal         error
}

func (r *archiveManifestReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.terminal != nil {
		return 0, r.terminal
	}
	if r.remaining == 0 {
		var probe [1]byte
		n, err := r.source.Read(probe[:])
		if n != 0 {
			r.terminal = archiveManifestLimitError(r.limit)
			return 0, r.terminal
		}
		r.terminal = err
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.source.Read(p)
	r.remaining -= int64(n)
	r.terminal = err
	return n, err
}

func archiveManifestLimitError(limit int64) error {
	return locale.Errorf("%w (limit=%d bytes)", ErrIndexBudget, limit)
}

func decodeArchiveSnapshot(source io.Reader, limit int64) (ArchiveSnapshot, error) {
	var snapshot ArchiveSnapshot
	if limit < 0 {
		return snapshot, archiveManifestLimitError(limit)
	}
	decoder := json.NewDecoder(&archiveManifestReader{source: source, remaining: limit, limit: limit})
	decoder.UseNumber() // Unknown numeric fields must not require float64 conversion.
	budget := archiveDecodeBudget{remaining: limit, limit: limit}
	if err := decodeArchiveObject(decoder, &snapshot, &budget); err != nil {
		return ArchiveSnapshot{}, err
	}
	// Unmarshal previously required one complete JSON value. Checking another
	// token preserves that rule and consumes trailing whitespace under the cap.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return ArchiveSnapshot{}, err
		}
		return ArchiveSnapshot{}, errors.New("traffic archive manifest contains trailing JSON data")
	}
	return snapshot, nil
}

type archiveDecodeBudget struct {
	remaining, limit int64
}

func (b *archiveDecodeBudget) charge(size int64) error {
	if size < 0 || size > b.remaining {
		return archiveManifestLimitError(b.limit)
	}
	b.remaining -= size
	return nil
}

func decodeArchiveObject(decoder *json.Decoder, snapshot *ArchiveSnapshot, budget *archiveDecodeBudget) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return nil // Preserve JSON null; version validation rejects it later.
	}
	if token != json.Delim('{') {
		return errors.New("traffic archive manifest must be an object")
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("invalid traffic archive manifest field")
		}
		switch {
		case strings.EqualFold(key, "version"):
			err = decoder.Decode(&snapshot.Version)
		case strings.EqualFold(key, "exchanges"):
			snapshot.Exchanges, err = decodeArchiveExchanges(decoder, budget)
		case strings.EqualFold(key, "blobs"):
			snapshot.Blobs, err = decodeArchiveHashes(decoder, budget)
		default:
			err = skipArchiveValue(decoder)
		}
		if err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func startArchiveArray(decoder *json.Decoder) (bool, error) {
	token, err := decoder.Token()
	if err != nil || token == nil {
		return false, err
	}
	if token != json.Delim('[') {
		return false, errors.New("traffic archive collection must be an array")
	}
	return true, nil
}

func decodeArchiveExchanges(decoder *json.Decoder, budget *archiveDecodeBudget) ([]ArchiveExchange, error) {
	array, err := startArchiveArray(decoder)
	if err != nil || !array {
		return nil, err
	}
	items := []ArchiveExchange{}
	for decoder.More() {
		// Reserve backing-array growth before decoding another complete record.
		// Three value widths cover the retained slice and growth headroom.
		if err := budget.charge(3 * int64(reflect.TypeOf(ArchiveExchange{}).Size())); err != nil {
			return nil, err
		}
		var item ArchiveExchange
		if err := decoder.Decode(&item); err != nil {
			return nil, err
		}
		for _, value := range []string{item.ID, item.Host, item.Method, item.URLTemplate, item.URL, item.ContentType, item.ReqHead, item.ReqBlob, item.RespHead, item.RespBlob} {
			if err := budget.charge(int64(len(value))); err != nil {
				return nil, err
			}
		}
		for _, body := range [][]byte{item.ReqBody, item.RespBody} {
			if err := budget.charge(int64(cap(body))); err != nil {
				return nil, err
			}
		}
		items = append(items, item)
	}
	_, err = decoder.Token()
	return items, err
}

func decodeArchiveHashes(decoder *json.Decoder, budget *archiveDecodeBudget) ([]string, error) {
	array, err := startArchiveArray(decoder)
	if err != nil || !array {
		return nil, err
	}
	items := []string{}
	for decoder.More() {
		if err := budget.charge(3 * int64(reflect.TypeOf("").Size())); err != nil {
			return nil, err
		}
		var hash string
		if err := decoder.Decode(&hash); err != nil {
			return nil, err
		}
		if err := budget.charge(int64(len(hash))); err != nil {
			return nil, err
		}
		items = append(items, hash)
	}
	_, err = decoder.Token()
	return items, err
}

// Ignore future fields without creating maps/slices for unknown collections.
// Tokens still pass through JSON syntax validation and the raw-byte limit.
func skipArchiveValue(decoder *json.Decoder) error {
	depth := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
				if depth > 10000 {
					return errors.New("traffic archive JSON exceeds maximum nesting depth")
				}
			case '}', ']':
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}

package traffic

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/privatefs"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ArchiveSnapshot is the portable traffic subset embedded in one task archive.
// Large content-addressed bodies are copied alongside this manifest rather than
// base64-encoded, preserving deduplication and allowing the tar writer to stream.
type ArchiveSnapshot struct {
	Version   int               `json:"version"`
	Exchanges []ArchiveExchange `json:"exchanges"`
	Blobs     []string          `json:"blobs"`
}

type ArchiveExchange struct {
	ID          string `json:"id"`
	TS          int64  `json:"ts"`
	Host        string `json:"host"`
	Method      string `json:"method"`
	URLTemplate string `json:"url_template"`
	URL         string `json:"url"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	ReqLen      int    `json:"req_len"`
	RespLen     int    `json:"resp_len"`
	ReqHead     string `json:"req_head"`
	ReqBody     []byte `json:"req_body,omitempty"`
	ReqBlob     string `json:"req_blob,omitempty"`
	RespHead    string `json:"resp_head"`
	RespBody    []byte `json:"resp_body,omitempty"`
	RespBlob    string `json:"resp_blob,omitempty"`
}

// ExportHosts writes an exact-host snapshot to dir. A SQLite read transaction
// preserves rows while archiveFiles retains their immutable body files.
func (t *Traffic) ExportHosts(hosts []string, dir string) (int64, error) {
	return t.exportHosts(hosts, dir, copyArchiveBlob)
}

// ExportHostsWithWriter lets archive staging apply one shared write allowance
// to the manifest and body copies. Ordinary exports keep their existing API.
func (t *Traffic) ExportHostsWithWriter(hosts []string, dir string, wrap func(io.Writer) io.Writer) (int64, error) {
	return t.exportHosts(hosts, dir, func(source, destination string) error {
		return copyArchiveBlobWithWriter(source, destination, wrap)
	}, wrap)
}

func (t *Traffic) exportHosts(hosts []string, dir string, copyBlob func(string, string) error, wrappers ...func(io.Writer) io.Writer) (int64, error) {
	if t == nil || len(hosts) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return 0, err
	}
	t.archiveFiles.RLock()
	defer t.archiveFiles.RUnlock()
	tx, err := t.beginArchiveSnapshot()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() // read-only snapshot, including all error paths
	unique := uniqueArchiveHosts(hosts)
	placeholders := make([]string, len(unique))
	args := make([]any, len(unique))
	for i, host := range unique {
		placeholders[i] = "?"
		args[i] = host
	}
	rows, err := tx.Query(`SELECT e.id,e.ts,e.host,e.method,e.url_template,e.url,e.status,e.content_type,e.req_len,e.resp_len,e.path,
b.id,COALESCE(b.req_head,''),b.req_body,b.req_blob,COALESCE(b.resp_head,''),b.resp_body,b.resp_blob
FROM exchanges e LEFT JOIN exchange_bodies b ON b.id=e.id
WHERE e.host IN (`+strings.Join(placeholders, ",")+`) ORDER BY e.ts,e.id`, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	manifest, err := privatefs.CreateTemp(dir, ".traffic-manifest-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = manifest.Close(); _ = os.Remove(manifest.Name()) }()
	var manifestWriter io.Writer = manifest
	if len(wrappers) > 0 && wrappers[0] != nil {
		manifestWriter = wrappers[0](manifest)
	}
	if _, err := io.WriteString(manifestWriter, `{"version":1,"exchanges":[`); err != nil {
		return 0, err
	}
	encoder := json.NewEncoder(manifestWriter)
	var count int64
	for rows.Next() {
		var item ArchiveExchange
		var legacyPath string
		var bodyID, reqBlob, respBlob sql.NullString
		if err := rows.Scan(&item.ID, &item.TS, &item.Host, &item.Method, &item.URLTemplate,
			&item.URL, &item.Status, &item.ContentType, &item.ReqLen, &item.RespLen, &legacyPath,
			&bodyID, &item.ReqHead, &item.ReqBody, &reqBlob, &item.RespHead, &item.RespBody, &respBlob); err != nil {
			return 0, err
		}
		if !bodyID.Valid && strings.TrimSpace(legacyPath) != "" {
			req, readErr := os.ReadFile(filepath.Join(t.dir, legacyPath, "request.http"))
			if readErr != nil && !os.IsNotExist(readErr) {
				return 0, readErr
			}
			resp, readErr := os.ReadFile(filepath.Join(t.dir, legacyPath, "response.http"))
			if readErr != nil && !os.IsNotExist(readErr) {
				return 0, readErr
			}
			item.ReqHead, item.RespHead = string(req), string(resp)
		} else if !bodyID.Valid {
			return 0, sql.ErrNoRows
		}
		item.ReqBlob, item.RespBlob = reqBlob.String, respBlob.String
		if count > 0 {
			if _, err := io.WriteString(manifestWriter, ","); err != nil {
				return 0, err
			}
		}
		if err := encoder.Encode(item); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if _, err := io.WriteString(manifestWriter, `],"blobs":[`); err != nil {
		return 0, err
	}
	// Let SQLite deduplicate/sort the small hash index instead of retaining a
	// Go map and a second slice proportional to the archived collection.
	blobRows, err := tx.Query(`WITH selected AS (
SELECT b.req_blob,b.resp_blob FROM exchange_bodies b JOIN exchanges e ON e.id=b.id
WHERE e.host IN (`+strings.Join(placeholders, ",")+`))
SELECT hash FROM (SELECT req_blob AS hash FROM selected UNION SELECT resp_blob AS hash FROM selected)
WHERE hash IS NOT NULL AND hash<>'' ORDER BY hash`, args...)
	if err != nil {
		return 0, err
	}
	defer blobRows.Close()
	var blobCount int64
	for blobRows.Next() {
		var hash string
		if err := blobRows.Scan(&hash); err != nil {
			return 0, err
		}
		source, err := t.blobPath(hash)
		if err != nil {
			return 0, err
		}
		if err := copyBlob(source, filepath.Join(dir, "blobs", hash+".bin")); err != nil {
			return 0, err
		}
		if blobCount > 0 {
			if _, err := io.WriteString(manifestWriter, ","); err != nil {
				return 0, err
			}
		}
		if err := encoder.Encode(hash); err != nil {
			return 0, err
		}
		blobCount++
	}
	if err := blobRows.Err(); err != nil {
		return 0, err
	}
	if _, err := io.WriteString(manifestWriter, "]}"); err != nil {
		return 0, err
	}
	if err := errors.Join(manifest.Sync(), manifest.Close()); err != nil {
		return 0, err
	}
	if err := os.Rename(manifest.Name(), filepath.Join(dir, "traffic.json")); err != nil {
		return 0, err
	}
	return count, nil
}

// The caller holds archiveFiles for reading. Establish the WAL snapshot under
// wmu so its row references and already-written blobs agree, then let capture
// continue while the potentially slow archive output runs.
func (t *Traffic) beginArchiveSnapshot() (*sql.Tx, error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	tx, err := t.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	var id string
	if err := tx.QueryRow(`SELECT id FROM exchanges LIMIT 1`).Scan(&id); err != nil && !errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// Copy body files without allocating a slice proportional to body size. A
// failed copy never publishes a partial blob at its content-addressed name.
func copyArchiveBlob(source, destination string) error {
	return copyArchiveBlobWithWriter(source, destination, nil)
}

func copyArchiveBlobWithWriter(source, destination string, wrap func(io.Writer) io.Writer) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := privatefs.CreateTemp(filepath.Dir(destination), ".traffic-blob-*")
	if err != nil {
		return err
	}
	defer func() { _ = output.Close(); _ = os.Remove(output.Name()) }()
	var writer io.Writer = output
	if wrap != nil {
		writer = wrap(output)
	}
	_, copyErr := io.Copy(writer, input)
	if err := errors.Join(copyErr, output.Sync(), output.Close()); err != nil {
		return err
	}
	return os.Rename(output.Name(), destination)
}

// ImportArchive imports only missing exchange IDs. Current hot rows always win,
// and repeated restore attempts are safe after a partial external failure.
// The returned count includes committed rows only.
func (t *Traffic) ImportArchive(dir string) (imported int64, resultErr error) {
	if t == nil {
		return 0, nil
	}
	snapshot, err := readArchiveSnapshot(filepath.Join(dir, "traffic.json"), t.indexLimit)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if snapshot.Version != 1 {
		return 0, locale.Errorf("unsupported traffic archive version %d", snapshot.Version)
	}
	t.wmu.Lock()
	defer t.wmu.Unlock()
	var created []storedBody
	t.writeReserveActive.Store(true)
	defer t.writeReserveActive.Store(false)
	committed := false
	defer func() {
		if !committed {
			imported = 0
			t.undoNewBlobs(created...)
		}
	}()
	var estimate int64
	for _, item := range snapshot.Exchanges {
		var exists int
		if err := t.db.QueryRow(`SELECT count(*) FROM exchanges WHERE id=?`, item.ID).Scan(&exists); err != nil {
			return 0, err
		}
		if exists != 0 {
			continue
		}
		// Imported bodies are indexed verbatim, so include their complete size.
		cost := (int64(len(item.URL))+int64(len(item.ReqHead))+int64(len(item.RespHead))+2*int64(len(item.ReqBody))+2*int64(len(item.RespBody)))*16 + 4096
		if cost > t.indexLimit || estimate > t.indexLimit-cost {
			return 0, locale.Errorf("%w (limit=%d bytes)", ErrIndexBudget, t.indexLimit)
		}
		estimate += cost
	}
	if estimate != 0 {
		estimate += 128 * 1024
	}
	if err := t.checkIndexAdmission(estimate); err != nil {
		return 0, err
	}
	for _, hash := range snapshot.Blobs {
		if !blobHashRe.MatchString(hash) {
			return 0, locale.Errorf("invalid archived traffic blob %q", hash)
		}
		fresh, err := t.importArchiveBlob(hash, filepath.Join(dir, "blobs", hash+".bin"))
		if err != nil {
			return 0, err
		}
		if fresh {
			created = append(created, storedBody{hash: hash, newBlob: true})
		}
	}
	if estimate > 0 {
		if err := t.checkFreeSpace(estimate); err != nil {
			return 0, err
		}
	}
	tx, err := t.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, item := range snapshot.Exchanges {
		res, err := tx.Exec(`INSERT OR IGNORE INTO exchanges(id,ts,host,method,url_template,url,status,content_type,req_len,resp_len,path)
VALUES(?,?,?,?,?,?,?,?,?,?,'')`, item.ID, item.TS, item.Host, item.Method, item.URLTemplate,
			item.URL, item.Status, item.ContentType, item.ReqLen, item.RespLen)
		if err != nil {
			return imported, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO exchange_bodies(id,req_head,req_body,req_blob,resp_head,resp_body,resp_blob)
VALUES(?,?,?,?,?,?,?)`, item.ID, item.ReqHead, item.ReqBody, nullIfEmpty(item.ReqBlob),
			item.RespHead, item.RespBody, nullIfEmpty(item.RespBlob)); err != nil {
			return imported, err
		}
		for _, hash := range []string{item.ReqBlob, item.RespBlob} {
			if hash != "" {
				if _, err := tx.Exec(`INSERT OR IGNORE INTO blob_refs(hash,exchange_id) VALUES(?,?)`, hash, item.ID); err != nil {
					return imported, err
				}
			}
		}
		if t.fts {
			var rowID int64
			if err := tx.QueryRow(`SELECT rowid FROM exchanges WHERE id=?`, item.ID).Scan(&rowID); err != nil {
				return imported, err
			}
			content := strings.Join([]string{item.URL, item.ReqHead, string(item.ReqBody), item.RespHead, string(item.RespBody)}, "\n")
			if _, err := tx.Exec(`INSERT INTO ex_fts(rowid,content) VALUES(?,?)`, rowID, content); err != nil {
				return imported, err
			}
		}
		imported++
	}
	if err := tx.Commit(); err != nil {
		return imported, err
	}
	committed = true
	return imported, nil
}

func uniqueArchiveHosts(hosts []string) []string {
	seen := make(map[string]struct{}, len(hosts))
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

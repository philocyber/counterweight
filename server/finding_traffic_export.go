package server

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Philocyber/counterweight/db"
	"github.com/Philocyber/counterweight/evidence"
	"github.com/Philocyber/counterweight/locale"
	"github.com/Philocyber/counterweight/privatefs"
	"github.com/Philocyber/counterweight/report"
)

func writeFindingsEvidenceZip(out io.Writer, findings []*db.DBFinding, stage string, now time.Time, langs ...locale.Lang) error {
	zw := zip.NewWriter(out)
	if err := writeFindingsZipEntries(zw, findings, stage, now, langs...); err != nil {
		zw.Close()
		return err
	}
	return zw.Close()
}

func writeFindingsZipEntries(zw *zip.Writer, findings []*db.DBFinding, stage string, now time.Time, langs ...locale.Lang) error {
	store := evidence.New(nil, nil, stage) // private export copy; not subject to GC
	used := map[string]int{}
	for _, f := range findings {
		base := report.FindingFilename(f)
		name := base
		if n := used[base]; n > 0 {
			name = fmt.Sprintf("%s-%d.md", strings.TrimSuffix(base, ".md"), n+1)
		}
		used[base]++
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err = io.WriteString(w, report.SingleFindingMarkdown(f, now, locale.First(langs))); err != nil {
			return err
		}
		for _, b := range f.TrafficBindings {
			prefix := fmt.Sprintf("evidence/%d/%d/", f.ID, b.ID)
			w, err := zw.Create(prefix + "manifest.json")
			if err != nil {
				return err
			}
			if err = json.NewEncoder(w).Encode(b); err != nil {
				return err
			}
			for _, side := range []string{"request", "response"} {
				if err := func() error {
					body, _, err := store.OpenBody(b.Snapshot, side)
					if err != nil {
						return err
					}
					defer body.Close()
					w, err := zw.Create(prefix + side + ".http")
					if err != nil {
						return err
					}
					head := b.Snapshot.ReqHead
					if side == "response" {
						head = b.Snapshot.RespHead
					}
					if _, err = io.WriteString(w, strings.TrimRight(head, "\r\n")+"\n\n"); err != nil {
						return err
					}
					if _, err = io.Copy(w, body); err != nil {
						return err
					}
					if _, err = body.Seek(0, io.SeekStart); err != nil {
						return err
					}
					w, err = zw.Create(prefix + side + ".bin")
					if err != nil {
						return err
					}
					_, err = io.Copy(w, body)
					return err
				}(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func buildFindingsEvidenceZip(path string, findings []*db.DBFinding, stage string, now time.Time, langs ...locale.Lang) error {
	f, err := privatefs.Create(path)
	if err != nil {
		return err
	}
	writeErr := writeFindingsEvidenceZip(f, findings, stage, now, langs...)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	// Re-read every compressed entry to validate the finished ZIP, including CRCs,
	// before the caller starts an HTTP download response.
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		r, err := entry.Open()
		if err != nil {
			return err
		}
		_, readErr := io.Copy(io.Discard, r)
		if err := errors.Join(readErr, r.Close()); err != nil {
			return err
		}
	}
	return nil
}

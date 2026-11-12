package server

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	pgdb "github.com/Philocyber/counterweight/db"
	"github.com/klauspost/compress/zstd"
)

func TestTaskArchiveExtractionBudgetPreservesPackageAndAllowsRetry(t *testing.T) {
	var compressed bytes.Buffer
	encoder, err := zstd.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	tarWriter := tar.NewWriter(encoder)
	body := bytes.Repeat([]byte{0x7b}, 2048)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "evidence.bin", Mode: 0600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "retained.tar.zst")
	if err := os.WriteFile(path, compressed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TASK_ARCHIVE_BYTES", "1024")
	destination := t.TempDir()
	if err := extractTaskArchivePackage(path, "", destination); !errors.Is(err, errTaskArchiveBudget) {
		t.Fatalf("oversized expansion accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "evidence.bin")); !os.IsNotExist(err) {
		t.Fatalf("over-budget entry was partially written: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, compressed.Bytes()) {
		t.Fatalf("rejection changed original package: %v", err)
	}
	t.Setenv("COUNTERWEIGHT_TASK_ARCHIVE_BYTES", "4096")
	if err := extractTaskArchivePackage(path, "", destination); err != nil {
		t.Fatalf("larger configured capacity cannot restore existing package: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "evidence.bin")); err != nil || !bytes.Equal(data, body) {
		t.Fatalf("retry changed restored bytes: %v", err)
	}
}

func TestArchiveStagingSharedBudgetBoundsTrafficAndEvidenceCopies(t *testing.T) {
	s, finding, _ := trafficEvidenceServer(t)
	body := bytes.Repeat([]byte{0x7b}, 100*1024)
	seedServerEvidenceFlow(t, s, "stage-copy", body)
	bindings, err := s.evidenceStore().Bind(context.Background(), finding.FindingID, []pgdb.TrafficRef{{TrafficID: "stage-copy"}})
	if err != nil {
		t.Fatal(err)
	}
	budget := &archiveStageBudget{limit: 65536, used: 32768}
	trafficDir := t.TempDir()
	if _, err := s.m.traffic.ExportHostsWithWriter([]string{"evidence.local"}, trafficDir, budget.wrap); !errors.Is(err, errTaskArchiveBudget) {
		t.Fatalf("traffic export ignored remaining stage capacity: %v", err)
	}
	if _, err := os.Stat(filepath.Join(trafficDir, "traffic.json")); !os.IsNotExist(err) {
		t.Fatalf("failed traffic manifest published: %v", err)
	}
	if n, err := s.m.traffic.Count(); err != nil || n != 1 {
		t.Fatalf("bounded export lost hot traffic: %d %v", n, err)
	}
	evidenceDir := t.TempDir()
	snapshots := []pgdb.TrafficEvidenceSnapshot{bindings.Bindings[0].Snapshot}
	if err := s.evidenceStore().CopySnapshotsWithWriter(context.Background(), snapshots, evidenceDir, budget.wrap); !errors.Is(err, errTaskArchiveBudget) {
		t.Fatalf("evidence copy ignored shared capacity: %v", err)
	}
	if budget.used > budget.limit {
		t.Fatalf("combined staging overshot limit: %d", budget.used)
	}
	if err := filepath.WalkDir(evidenceDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(data) != 0 {
			t.Errorf("failed evidence copy left a partial body: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Reuse the same destination: a failed hash-named partial file must not make
	// the retry fail verification forever. Existing complete bodies are reused.
	retry := &archiveStageBudget{limit: 256 * 1024}
	if err := s.evidenceStore().CopySnapshotsWithWriter(context.Background(), snapshots, evidenceDir, retry.wrap); err != nil {
		t.Fatalf("evidence copy cannot retry: %v", err)
	}
	reader, _, err := s.evidenceStore().OpenBody(snapshots[0], "response")
	if err != nil {
		t.Fatal(err)
	}
	saved, readErr := io.ReadAll(reader)
	reader.Close()
	if readErr != nil || !bytes.Equal(saved, body) {
		t.Fatalf("source evidence changed: %v", readErr)
	}
}

func TestTaskArchiveWorkerQuotaFailurePreservesHotTaskAndRetry(t *testing.T) {
	s, finding, _ := trafficEvidenceServer(t)
	s.ctx = context.Background()
	record, err := s.m.pg.GetFinding(finding.FindingID)
	if err != nil {
		t.Fatal(err)
	}
	tid := *record.TaskID
	source := filepath.Join(s.m.dir, "tasks", strconv.FormatInt(tid, 10), "uploads", "quota-proof.bin")
	body := bytes.Repeat([]byte{0x7b}, 4096)
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.m.SetTaskPaused(strconv.FormatInt(tid, 10), true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TASK_ARCHIVE_BYTES", "1024")
	job, err := s.m.pg.QueueTaskArchive(tid)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.runOneTaskArchiveJob(); !errors.Is(err, errTaskArchiveBudget) {
		t.Fatalf("worker did not surface quota failure: %v", err)
	}
	if data, err := os.ReadFile(source); err != nil || !bytes.Equal(data, body) {
		t.Fatalf("worker lost staged hot file: %v", err)
	}
	if got, err := s.m.pg.GetFinding(finding.FindingID); err != nil || got == nil {
		t.Fatalf("worker deleted finding on failed archive: %v", err)
	}
	if _, err := os.Stat(taskArchivePath(s.m.dir, job.ID, strconv.FormatInt(tid, 10))); !os.IsNotExist(err) {
		t.Fatalf("failed worker published archive: %v", err)
	}
	t.Setenv("COUNTERWEIGHT_TASK_ARCHIVE_BYTES", "1048576")
	if _, err := s.m.pg.QueueTaskArchive(tid); err != nil {
		t.Fatal(err)
	}
	if err := s.runOneTaskArchiveJob(); err != nil {
		t.Fatalf("quota-failed task cannot retry: %v", err)
	}
	if got, err := s.m.pg.GetFinding(finding.FindingID); err != nil || got != nil {
		t.Fatalf("successful archive did not retire hot finding: %v", err)
	}
	if _, err := s.m.pg.QueueTaskArchiveRestore(job.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.runOneTaskArchiveJob(); err != nil {
		t.Fatalf("quota retry archive cannot restore: %v", err)
	}
	if data, err := os.ReadFile(source); err != nil || !bytes.Equal(data, body) {
		t.Fatalf("worker round trip changed bytes: %v", err)
	}
}

func TestTaskArchivePackagingBudgetRestoresStagedFiles(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "tasks", "42", "uploads", "evidence.bin")
	body := bytes.Repeat([]byte{0x7b}, 4096)
	if err := os.MkdirAll(filepath.Dir(source), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, body, 0600); err != nil {
		t.Fatal(err)
	}
	stage, err := stageTaskArchiveFiles(root, 1, "42", 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("COUNTERWEIGHT_TASK_ARCHIVE_BYTES", "1024")
	path := taskArchivePath(root, 1, "42")
	if _, _, _, err := writeTaskArchivePackage(path, stage.payload, &pgdb.TaskArchiveSnapshot{FormatVersion: pgdb.TaskArchiveFormatVersion, TaskID: 42}); !errors.Is(err, errTaskArchiveBudget) {
		t.Fatalf("oversized payload accepted: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed package published: %v", err)
	}
	if _, err := os.Stat(path + ".partial"); !os.IsNotExist(err) {
		t.Fatalf("failed package leaked staging data: %v", err)
	}
	if err := stage.rollback(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(source); err != nil || !bytes.Equal(data, body) {
		t.Fatalf("quota failure broke staging rollback: %v", err)
	}
}

func TestTaskArchiveByteWriterRejectsBeforeGrowingStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	w := newArchiveByteWriter(file, 8)
	if n, err := w.Write([]byte("saved")); err != nil || n != 5 {
		t.Fatalf("bounded write: %d %v", n, err)
	}
	if n, err := w.Write([]byte("overflow")); n != 0 || !errors.Is(err, errTaskArchiveBudget) {
		t.Fatalf("stage writer exceeded capacity: %d %v", n, err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "saved" {
		t.Fatalf("rejected write touched staging bytes: %v", err)
	}
}

func TestTaskArchiveCapacityConfiguration(t *testing.T) {
	for _, raw := range []string{"0", "-1", "invalid", strconv.FormatInt(maxArchiveBytes+1, 10), "9223372036854775808"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("COUNTERWEIGHT_TASK_ARCHIVE_BYTES", raw)
			if _, err := taskArchiveByteLimit(); err == nil {
				t.Fatal("invalid capacity accepted")
			}
		})
	}
	t.Setenv("COUNTERWEIGHT_TASK_ARCHIVE_BYTES", "")
	if limit, err := taskArchiveByteLimit(); err != nil || limit != defaultTaskArchiveBytes {
		t.Fatalf("default: %d %v", limit, err)
	}
}

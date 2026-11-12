package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Philocyber/counterweight/traffic"
	"github.com/jackc/pgx/v5"
)

func TestTrafficBlobStorageStatusAPI(t *testing.T) {
	if os.Getenv("COUNTERWEIGHT_TRAFFIC_STORAGE_E2E") != "1" {
		t.Skip("requires explicit disposable transport database")
	}
	config, err := pgx.ParseConfig(os.Getenv("COUNTERWEIGHT_PG_DSN"))
	if err != nil || !strings.HasPrefix(config.Database, "counterweight_transport_") {
		t.Fatal("explicit disposable transport database required")
	}
	t.Setenv("COUNTERWEIGHT_TRAFFIC_BLOB_BYTES", "7")
	t.Setenv("COUNTERWEIGHT_TRAFFIC_WAL_BYTES", "262144")
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "COUNTERWEIGHT_LLM_PROVIDER", "COUNTERWEIGHT_LLM_MODEL", "COUNTERWEIGHT_LLM_BASE_URL", "COUNTERWEIGHT_LLM_PROXY"} {
		t.Setenv(key, "")
	}
	m, err := NewManager(t.TempDir(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.Traffic() == nil {
		t.Fatal("recording store not initialized")
	}
	archive := t.TempDir()
	os.Mkdir(filepath.Join(archive, "blobs"), 0700)
	body := []byte("fixture")
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(archive, "blobs", hash+".bin"), body, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(traffic.ArchiveSnapshot{Version: 1, Blobs: []string{hash}})
	if err := os.WriteFile(filepath.Join(archive, "traffic.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Traffic().ImportArchive(archive); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, m, dir, dir, dir)
	token, err := signTestSession(t, s)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/traffic", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, req)
	var payload struct {
		Storage traffic.BlobStorageStatus `json:"blob_storage"`
		WAL     traffic.WALStorageStatus  `json:"wal_storage"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload.Storage.LimitBytes != 7 || payload.Storage.UsedBytes != 7 || payload.Storage.DroppedCaptures != 0 {
		t.Fatalf("authenticated storage status lost actual usage: %d %s", response.Code, response.Body.String())
	}
	actual, err := m.Traffic().WALStorageStatus()
	if err != nil || payload.WAL.AdmissionBytes != 262144 || payload.WAL != actual {
		t.Fatalf("authenticated WAL status lost actual usage: %+v %+v %v", payload.WAL, actual, err)
	}
}

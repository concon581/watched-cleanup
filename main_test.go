package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/concon581/watched-cleanup/models"
)

func TestGlobalDryRunEnabled(t *testing.T) {
	t.Setenv("DRY_RUN_MODE", "true")
	if !globalDryRunEnabled() {
		t.Fatal("expected true to enable global dry-run")
	}

	t.Setenv("DRY_RUN_MODE", "1")
	if !globalDryRunEnabled() {
		t.Fatal("expected 1 to enable global dry-run")
	}

	t.Setenv("DRY_RUN_MODE", " YES ")
	if !globalDryRunEnabled() {
		t.Fatal("expected yes with whitespace to enable global dry-run")
	}
}

func TestGlobalDryRunDisabled(t *testing.T) {
	t.Setenv("DRY_RUN_MODE", "")
	if globalDryRunEnabled() {
		t.Fatal("expected empty DRY_RUN_MODE to disable global dry-run")
	}

	t.Setenv("DRY_RUN_MODE", "false")
	if globalDryRunEnabled() {
		t.Fatal("expected false to disable global dry-run")
	}
}

func TestOrphanDeleteHonorsGlobalDryRun(t *testing.T) {
	t.Setenv("DRY_RUN_MODE", "true")

	dir := t.TempDir()
	target := filepath.Join(dir, "orphan.mkv")
	if err := os.WriteFile(target, []byte("media"), 0600); err != nil {
		t.Fatalf("write orphan file: %v", err)
	}

	orphanMutex.Lock()
	orphanCache = models.OrphanScanResponse{
		TorrentOrphans: []models.OrphanFileEntry{{
			Path:   target,
			SizeGB: 0.001,
		}},
		TorrentOrphansGB: 0.001,
	}
	orphanScanning = false
	orphanMutex.Unlock()

	scanMutex.Lock()
	scanStatus = models.ScanStatus{}
	scanMutex.Unlock()

	body := bytes.NewBufferString(`{"zone":"torrents","paths":[` + strconv.Quote(target) + `],"dryRun":false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/orphan-delete", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleOrphanDelete(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected global dry-run to preserve file: %v", err)
	}

	var resp orphanDeleteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.DryRun {
		t.Fatal("expected response to report dry-run")
	}
	if len(resp.Deleted) != 1 || len(resp.Failed) != 0 {
		t.Fatalf("unexpected delete response: %+v", resp)
	}
}

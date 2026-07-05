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

func TestNormalizeDeleteType(t *testing.T) {
	tests := map[string]string{
		"movie":    "movie",
		"movies":   "movie",
		" Movie ":  "movie",
		"season":   "season",
		"seasons":  "season",
		"episode":  "",
		"":         "",
		"../../tv": "",
	}
	for input, want := range tests {
		if got := normalizeDeleteType(input); got != want {
			t.Fatalf("normalizeDeleteType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDeletePreviewRejectsInvalidType(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/delete-preview?type=episode&ids=abc123", nil)
	w := httptest.NewRecorder()

	handleDeletePreview(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
}

func TestDeleteConfirmRejectsInvalidType(t *testing.T) {
	deleteMutex.Lock()
	isDeleting = false
	deleteMutex.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/delete-confirm", bytes.NewBufferString("type=episode&ids=abc123"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	handleDeleteConfirm(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", w.Code)
	}
	deleteMutex.RLock()
	deleting := isDeleting
	deleteMutex.RUnlock()
	if deleting {
		t.Fatal("invalid delete type should not start a delete")
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

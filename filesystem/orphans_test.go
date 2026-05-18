package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanOrphans_hardlinkPair(t *testing.T) {
	root := t.TempDir()
	torrents := filepath.Join(root, "torrents")
	library := filepath.Join(root, "movies")
	if err := os.MkdirAll(torrents, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(library, 0o755); err != nil {
		t.Fatal(err)
	}

	libFile := filepath.Join(library, "movie.mkv")
	if err := os.WriteFile(libFile, []byte("video-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	torFile := filepath.Join(torrents, "movie.mkv")
	if err := os.Link(libFile, torFile); err != nil {
		t.Fatal(err)
	}

	orphanOnly := filepath.Join(torrents, "leftover.mkv")
	if err := os.WriteFile(orphanOnly, []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ScanOrphans(torrents, []string{library})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.TorrentOrphans) != 1 {
		t.Fatalf("expected 1 torrent orphan, got %d", len(result.TorrentOrphans))
	}
	if result.TorrentOrphans[0].Path != orphanOnly {
		t.Fatalf("unexpected orphan path %s", result.TorrentOrphans[0].Path)
	}
	if len(result.LibraryOrphans) != 0 {
		t.Fatalf("expected 0 library orphans, got %d", len(result.LibraryOrphans))
	}
}

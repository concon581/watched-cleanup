package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildLinkIndex_lookupFindsOnlyHardlinks(t *testing.T) {
	root := t.TempDir()

	original := filepath.Join(root, "movie.mkv")
	if err := os.WriteFile(original, []byte("video-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "torrent-copy.mkv")
	if err := os.Link(original, linked); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(root, "other.mkv")
	if err := os.WriteFile(unrelated, []byte("different"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := BuildLinkIndex(root)
	if err != nil {
		t.Fatal(err)
	}

	paths, err := idx.Lookup(original)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 linked paths, got %d: %v", len(paths), paths)
	}
	for _, p := range paths {
		if p == unrelated {
			t.Fatalf("unrelated file matched as hardlink: %s", p)
		}
	}
}

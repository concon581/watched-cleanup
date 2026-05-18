package filesystem

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DiskEntry is a path on disk with its total size.
type DiskEntry struct {
	Name     string
	Path     string // relative to data root
	SizeGB   float64
	Category string // movies, tv, torrents, music, downloads, backup, other
}

// DirSizeBytes returns the total size of all files under root.
func DirSizeBytes(root string) (int64, error) {
	var size int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size, err
}

// ScanStorageBreakdown lists folders under dataPath (top-level, torrent children, and other subfolders).
func ScanStorageBreakdown(dataPath, torrentsPath string) ([]DiskEntry, error) {
	dataPath = filepath.Clean(dataPath)
	entries := []DiskEntry{}

	top, err := os.ReadDir(dataPath)
	if err != nil {
		return nil, err
	}

	torrentsPath = filepath.Clean(torrentsPath)

	for _, child := range top {
		name := child.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dataPath, name)
		if torrentsPath != "" && filepath.Clean(full) == torrentsPath {
			continue
		}
		size, err := DirSizeBytes(full)
		if err != nil || size <= 0 {
			continue
		}
		entries = append(entries, DiskEntry{
			Name:     name,
			Path:     name,
			SizeGB:   float64(size) / (1024 * 1024 * 1024),
			Category: categorizePath(name, full, torrentsPath),
		})
	}

	if torrentsPath != "" {
		if info, err := os.Stat(torrentsPath); err == nil && info.IsDir() {
			relRoot := relStoragePath(dataPath, torrentsPath)
			if relRoot != "" {
				entries = append(entries, scanChildren(torrentsPath, relRoot, "torrents")...)
			}
		}
	}

	expanded := make([]DiskEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Category != "other" || entry.SizeGB < 1.0 {
			expanded = append(expanded, entry)
			continue
		}
		full := filepath.Join(dataPath, entry.Path)
		children := scanChildren(full, entry.Path, "")
		if len(children) == 0 {
			expanded = append(expanded, entry)
			continue
		}
		expanded = append(expanded, children...)
	}

	sort.Slice(expanded, func(i, j int) bool {
		return expanded[i].SizeGB > expanded[j].SizeGB
	})

	return expanded, nil
}

func scanChildren(parentPath, relRoot, defaultCategory string) []DiskEntry {
	var entries []DiskEntry
	children, err := os.ReadDir(parentPath)
	if err != nil {
		return entries
	}

	for _, child := range children {
		if strings.HasPrefix(child.Name(), ".") {
			continue
		}
		full := filepath.Join(parentPath, child.Name())
		rel := filepath.Join(relRoot, child.Name())
		var size int64
		if child.IsDir() {
			size, _ = DirSizeBytes(full)
		} else {
			if info, err := os.Stat(full); err == nil {
				size = info.Size()
			}
		}
		if size <= 0 {
			continue
		}
		category := defaultCategory
		if category == "" {
			category = categorizePath(child.Name(), full, "")
		}
		entries = append(entries, DiskEntry{
			Name:     child.Name(),
			Path:     rel,
			SizeGB:   float64(size) / (1024 * 1024 * 1024),
			Category: category,
		})
	}
	return entries
}

func relStoragePath(dataPath, target string) string {
	rel, err := filepath.Rel(dataPath, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.Base(target)
	}
	return rel
}

func categorizePath(name, fullPath, torrentsPath string) string {
	torrentsPath = filepath.Clean(torrentsPath)
	fullPath = filepath.Clean(fullPath)

	if torrentsPath != "" {
		if fullPath == torrentsPath || strings.HasPrefix(fullPath, torrentsPath+string(os.PathSeparator)) {
			return "torrents"
		}
	}

	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "movie") || lower == "films" || lower == "cinema":
		return "movies"
	case strings.Contains(lower, "tv") || strings.Contains(lower, "series") || strings.Contains(lower, "show") || strings.Contains(lower, "television"):
		return "tv"
	case strings.Contains(lower, "torrent") || lower == "incoming" || lower == "seeding":
		return "torrents"
	case strings.Contains(lower, "music") || strings.Contains(lower, "flac") || strings.Contains(lower, "audio"):
		return "music"
	case strings.Contains(lower, "download"):
		return "downloads"
	case strings.Contains(lower, "backup") || strings.Contains(lower, "archive"):
		return "backup"
	case strings.Contains(lower, "usenet") || strings.Contains(lower, "nzb"):
		return "downloads"
	case strings.Contains(lower, "anime"):
		return "tv"
	default:
		return "other"
	}
}

func IsMediaLibraryCategory(category string) bool {
	return category == "movies" || category == "tv"
}

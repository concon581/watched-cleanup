package filesystem

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// OrphanFile is a media file with no hardlink in the other zone (torrents vs library).
type OrphanFile struct {
	Path   string
	SizeGB float64
	NLink  uint64
	Inode  uint64
}

// OrphanScanResult holds orphan files in torrents and in movie/TV libraries.
type OrphanScanResult struct {
	TorrentsPath  string
	LibraryPaths  []string
	TorrentOrphans []OrphanFile
	LibraryOrphans []OrphanFile
}

type inodeRecord struct {
	inode  uint64
	nlink  uint64
	size   int64
	paths  []string
}

// ScanOrphans finds files that exist only in torrents or only in library paths (by inode).
func ScanOrphans(torrentsPath string, libraryPaths []string) (OrphanScanResult, error) {
	result := OrphanScanResult{
		TorrentsPath: filepath.Clean(torrentsPath),
		LibraryPaths: append([]string{}, libraryPaths...),
	}

	torrentsPath = filepath.Clean(torrentsPath)
	cleanLibs := make([]string, 0, len(libraryPaths))
	for _, p := range libraryPaths {
		p = filepath.Clean(p)
		if p == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			cleanLibs = append(cleanLibs, p)
		}
	}
	result.LibraryPaths = cleanLibs

	byInode := make(map[uint64]*inodeRecord)

	walkRoots := []string{torrentsPath}
	walkRoots = append(walkRoots, cleanLibs...)

	for _, root := range walkRoots {
		if root == "" {
			continue
		}
		if _, err := os.Stat(root); err != nil {
			continue
		}
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return nil
			}
			ino := stat.Ino
			rec, exists := byInode[ino]
			if !exists {
				rec = &inodeRecord{inode: ino, nlink: uint64(stat.Nlink), size: info.Size()}
				byInode[ino] = rec
			}
			rec.paths = append(rec.paths, path)
			return nil
		})
	}

	for _, rec := range byInode {
		var inTorrents, inLibrary bool
		for _, p := range rec.paths {
			if isUnderPath(p, torrentsPath) {
				inTorrents = true
			}
			for _, lib := range cleanLibs {
				if isUnderPath(p, lib) {
					inLibrary = true
					break
				}
			}
		}

		file := OrphanFile{
			SizeGB: float64(rec.size) / (1024 * 1024 * 1024),
			NLink:  rec.nlink,
			Inode:  rec.inode,
		}

		if inTorrents && !inLibrary {
			for _, p := range rec.paths {
				if isUnderPath(p, torrentsPath) {
					file.Path = p
					result.TorrentOrphans = append(result.TorrentOrphans, file)
				}
			}
		}
		if inLibrary && !inTorrents {
			for _, p := range rec.paths {
				if pathInLibraries(p, cleanLibs) {
					file.Path = p
					result.LibraryOrphans = append(result.LibraryOrphans, file)
				}
			}
		}
	}

	sort.Slice(result.TorrentOrphans, func(i, j int) bool {
		return result.TorrentOrphans[i].SizeGB > result.TorrentOrphans[j].SizeGB
	})
	sort.Slice(result.LibraryOrphans, func(i, j int) bool {
		return result.LibraryOrphans[i].SizeGB > result.LibraryOrphans[j].SizeGB
	})
	return result, nil
}

func isUnderPath(path, root string) bool {
	if root == "" {
		return false
	}
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	return path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
}

func pathInLibraries(path string, libs []string) bool {
	for _, lib := range libs {
		if isUnderPath(path, lib) {
			return true
		}
	}
	return false
}

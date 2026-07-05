package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// FileID uniquely identifies a file across filesystems. Inode numbers are
// only unique per device, so both fields are required to match hardlinks
// safely when DATA_PATH spans multiple mounts (mergerfs, extra disks, NFS).
type FileID struct {
	Dev uint64
	Ino uint64
}

func fileIDFromStat(stat *syscall.Stat_t) FileID {
	return FileID{Dev: uint64(stat.Dev), Ino: uint64(stat.Ino)}
}

// GetFileID returns the device+inode identity of a file.
func GetFileID(path string) (FileID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return FileID{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return FileID{}, fmt.Errorf("failed to get stat info for %s", path)
	}
	return fileIDFromStat(stat), nil
}

// LinkIndex maps file identities to every path seen for them under a
// directory tree. Build it once per delete run instead of walking the
// torrents directory for every file being deleted.
type LinkIndex map[FileID][]string

// BuildLinkIndex walks searchDir once and records all regular files by FileID.
func BuildLinkIndex(searchDir string) (LinkIndex, error) {
	index := make(LinkIndex)
	err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil // Skip errors, continue walking
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		id := fileIDFromStat(stat)
		index[id] = append(index[id], path)
		return nil
	})
	return index, err
}

// Lookup returns all indexed paths that are hardlinks of targetPath.
func (idx LinkIndex) Lookup(targetPath string) ([]string, error) {
	id, err := GetFileID(targetPath)
	if err != nil {
		return nil, err
	}
	return idx[id], nil
}

// FindHardlinks finds all hardlinks to a target file by comparing device+inode
func FindHardlinks(targetPath string, searchDir string) ([]string, error) {
	targetID, err := GetFileID(targetPath)
	if err != nil {
		return nil, err
	}

	var matches []string

	// Walk the search directory
	err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		if info.IsDir() {
			return nil // Skip directories
		}

		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}

		// Same device and inode means this is a hardlink
		if fileIDFromStat(stat) == targetID {
			matches = append(matches, path)
		}

		return nil
	})

	return matches, err
}

// GetAllFilesInDir finds all files in a directory recursively
func GetAllFilesInDir(dirPath string) ([]string, error) {
	var files []string

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if !info.IsDir() {
			files = append(files, path)
		}

		return nil
	})

	return files, err
}

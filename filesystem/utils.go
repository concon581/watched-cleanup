package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// FindHardlinks finds all hardlinks to a target file by comparing inodes
func FindHardlinks(targetPath string, searchDir string) ([]string, error) {
	// Get the inode of the target file
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		return nil, err
	}

	targetStat, ok := targetInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("failed to get stat info")
	}
	targetInode := targetStat.Ino

	var matches []string

	// Walk the search directory
	err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors, continue walking
		}

		if info.IsDir() {
			return nil // Skip directories
		}

		// Get inode of current file
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}

		// If inodes match, this is a hardlink
		if stat.Ino == targetInode {
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

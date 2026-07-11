package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func EnsureDirectory(dir string, mode os.FileMode) error {
	dir = filepath.Clean(dir)
	anchor := dir
	for {
		info, err := os.Stat(anchor)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("storage directory %q is not a directory", anchor)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(anchor)
		if parent == anchor {
			return err
		}
		anchor = parent
	}

	if err := os.MkdirAll(dir, mode); err != nil {
		return err
	}
	if anchor == dir {
		return nil
	}
	for current := dir; ; current = filepath.Dir(current) {
		if err := SyncDirectory(current); err != nil {
			return fmt.Errorf("sync created storage directory %q: %w", current, err)
		}
		if current == anchor {
			return nil
		}
	}
}

func SyncDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

package backup

import (
	"os"
	"path/filepath"
)

// ensureParent creates parent directories for a file path (not empty leaf folders).
func ensureParent(filePath string) error {
	return os.MkdirAll(filepath.Dir(filePath), 0o755)
}

// pruneEmptyDirs removes dir if empty, then walks parents up to stopAt (exclusive).
func pruneEmptyDirs(dir, stopAt string) {
	for dir != "" && dir != stopAt && stringsHasPrefixPath(dir, stopAt) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		_ = os.Remove(dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func stringsHasPrefixPath(path, prefix string) bool {
	if prefix == "" {
		return true
	}
	cleanP := filepath.Clean(path)
	cleanPre := filepath.Clean(prefix)
	if cleanP == cleanPre {
		return true
	}
	return len(cleanP) > len(cleanPre) && cleanP[:len(cleanPre)] == cleanPre &&
		(cleanP[len(cleanPre)] == filepath.Separator || cleanPre == string(filepath.Separator))
}

func dirHasFiles(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if !d.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

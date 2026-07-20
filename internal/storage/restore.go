package storage

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExportZip restores a snapshot to a temp dir and returns a zip file path.
func (e *Engine) ExportZip(ctx context.Context, repoPath, password, snapshotID, workDir string) (zipPath string, err error) {
	if err := ValidateSnapshotID(snapshotID); err != nil {
		return "", err
	}
	dest, err := EnsureSubpath(workDir, "restore-"+snapshotID)
	if err != nil {
		return "", err
	}
	if err := e.Restore(ctx, repoPath, password, snapshotID, dest); err != nil {
		return "", err
	}
	zipPath, err = EnsureSubpath(workDir, snapshotID+".zip")
	if err != nil {
		return "", err
	}
	if err := zipDir(dest, zipPath); err != nil {
		return "", err
	}
	return zipPath, nil
}

func zipDir(src, destZip string) error {
	_, _, err := ZipDirCounted(src, destZip)
	return err
}

// ZipDirCounted zips src into destZip and returns file count + uncompressed bytes.
func ZipDirCounted(src, destZip string) (files int, nbytes int64, err error) {
	f, err := os.Create(destZip)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()
	err = filepath.Walk(src, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		n, copyErr := io.Copy(w, in)
		_ = in.Close()
		if copyErr != nil {
			return copyErr
		}
		files++
		nbytes += n
		return nil
	})
	return files, nbytes, err
}

// CollectFilesUnder returns regular file paths under root with a given prefix filter (optional).
func CollectFilesUnder(root, prefix string) ([]string, error) {
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if prefix != "" && !strings.HasPrefix(filepath.ToSlash(rel), strings.Trim(prefix, "/")) {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out, err
}

func EnsureSubpath(root, sub string) (string, error) {
	target := filepath.Join(root, filepath.Clean(sub))
	if !strings.HasPrefix(target, filepath.Clean(root)+string(os.PathSeparator)) && target != filepath.Clean(root) {
		return "", fmt.Errorf("invalid subpath")
	}
	return target, nil
}

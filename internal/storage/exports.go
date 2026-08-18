package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PSTExportRoot is {repo}/exports/pst
func PSTExportRoot(repoPath string) string {
	return filepath.Join(repoPath, "exports", "pst")
}

// PSTExportRun describes one completed (or in-progress) export batch.
type PSTExportRun struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Path      string    `json:"path"`
	Users     int       `json:"users"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Human     string    `json:"human"`
	Zips      []string  `json:"zips,omitempty"`  // downloadable *.zip basenames
	Scope     string    `json:"scope,omitempty"` // all | mailbox | folder
	Mailbox   string    `json:"mailbox,omitempty"`
	Folder    string    `json:"folder,omitempty"`
}

// ListPSTExports lists export run directories newest first.
func ListPSTExports(repoPath string) ([]PSTExportRun, error) {
	root := PSTExportRoot(repoPath)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []PSTExportRun
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		dir := filepath.Join(root, id)
		run := PSTExportRun{ID: id, Path: dir}
		if meta, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
			_ = json.Unmarshal(meta, &run)
			run.ID = id
			run.Path = dir
		} else {
			info, _ := e.Info()
			if info != nil {
				run.CreatedAt = info.ModTime().UTC()
			}
			run.Bytes, _ = DirSize(dir)
		}
		run.Human = FormatBytes(run.Bytes)
		ents, _ := os.ReadDir(dir)
		for _, fe := range ents {
			if fe.IsDir() {
				continue
			}
			name := fe.Name()
			if strings.HasSuffix(strings.ToLower(name), ".zip") {
				run.Zips = append(run.Zips, name)
			}
		}
		sort.Strings(run.Zips)
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// ApplyPSTExportRetention deletes older export runs beyond keepN.
func ApplyPSTExportRetention(repoPath string, keepN int) error {
	if keepN < 1 {
		keepN = 5
	}
	runs, err := ListPSTExports(repoPath)
	if err != nil {
		return err
	}
	for i := keepN; i < len(runs); i++ {
		_ = os.RemoveAll(runs[i].Path)
	}
	return nil
}

// WritePSTManifest writes export run metadata.
func WritePSTManifest(dir string, run PSTExportRun) error {
	run.Human = FormatBytes(run.Bytes)
	b, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600)
}

// SanitizeExportName turns a UPN into a safe directory/file name.
func SanitizeExportName(upn string) string {
	s := strings.TrimSpace(upn)
	s = strings.ReplaceAll(s, "@", "_at_")
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
	if s == "" {
		return "user"
	}
	return s
}

// EnsurePSTExportDir creates exports/pst and returns a new run directory.
func EnsurePSTExportDir(repoPath string) (runID, runDir string, err error) {
	runID = time.Now().UTC().Format("20060102T150405Z")
	runDir = filepath.Join(PSTExportRoot(repoPath), runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return "", "", fmt.Errorf("mkdir pst export: %w", err)
	}
	return runID, runDir, nil
}

package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
	"github.com/rhw/m365backup/internal/storage"
)

// PSTExport builds mailbox archives from the live Exchange sync tree into
// {repo}/exports/pst/{runID}/. Output is a ZIP of .eml files per user (Outlook /
// Thunderbird can import EMLs). True binary .pst is not produced (no OSS writer).
type PSTExport struct{}

func (PSTExport) Name() string { return "pst" }

func (PSTExport) Run(ctx context.Context, _ *graph.Client, tenant *db.Tenant, job *db.Job, _ string, _ TokenStore) (Result, error) {
	res := NewResult(ctx)
	res.SkipSnapshot = true

	syncRoot, ok := storage.LiveSyncRoot(tenant.KopiaRepoPath, "exchange")
	if !ok {
		return res, fmt.Errorf("kein Exchange Live-Sync vorhanden — zuerst Exchange-Backup ausführen")
	}
	entries, err := os.ReadDir(syncRoot)
	if err != nil {
		return res, err
	}

	runID, runDir, err := storage.EnsurePSTExportDir(tenant.KopiaRepoPath)
	if err != nil {
		return res, err
	}
	res.ExportPath = runDir
	res.Info(fmt.Sprintf("PST-Export nach %s", runDir))

	var users []os.DirEntry
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			users = append(users, e)
		}
	}
	if len(users) == 0 {
		return res, fmt.Errorf("Exchange Sync enthält keine Postfächer")
	}
	res.addTotal(len(users))

	var totalBytes int64
	var totalFiles int
	for i, u := range users {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		upn := u.Name()
		src := filepath.Join(syncRoot, upn)
		safe := storage.SanitizeExportName(upn)
		zipPath := filepath.Join(runDir, safe+".zip")

		pct := 5 + (i*90)/len(users)
		msg := fmt.Sprintf("Exportiere %s (%d/%d)…", upn, i+1, len(users))
		res.Info(msg)
		if p := ProgressFrom(ctx); p != nil {
			p.SyncJob(job, &res, pct, msg)
		}

		nFiles, nBytes, err := storage.ZipDirCounted(src, zipPath)
		if err != nil {
			res.Warn(fmt.Sprintf("%s: %v", upn, err))
			continue
		}
		if nFiles == 0 {
			_ = os.Remove(zipPath)
			res.Skip(upn)
			continue
		}
		totalFiles += nFiles
		totalBytes += nBytes
		res.addItems(1, nBytes)
		res.Info(fmt.Sprintf("%s → %s (%d Mails, %s)", upn, filepath.Base(zipPath), nFiles, storage.FormatBytes(nBytes)))
	}

	manifest := storage.PSTExportRun{
		ID:        runID,
		CreatedAt: time.Now().UTC(),
		Path:      runDir,
		Users:     res.ItemsNew,
		Files:     totalFiles,
		Bytes:     totalBytes,
	}
	if err := storage.WritePSTManifest(runDir, manifest); err != nil {
		res.Warn("manifest: " + err.Error())
	}

	if res.ItemsNew == 0 {
		_ = os.RemoveAll(runDir)
		return res, fmt.Errorf("keine Mails zum Exportieren gefunden")
	}

	res.Info(fmt.Sprintf("Fertig: %d Postfächer, %d Dateien, %s → exports/pst/%s",
		res.ItemsNew, totalFiles, storage.FormatBytes(totalBytes), runID))
	return res, nil
}

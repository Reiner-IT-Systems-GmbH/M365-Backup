package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rhw/m365backup/internal/catalog"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
	"github.com/rhw/m365backup/internal/storage"
)

// PSTExportParams selects what to include in a PST/EML-ZIP export run.
// Scope: "all" (default), "mailbox", or "folder".
type PSTExportParams struct {
	Scope   string `json:"scope"`
	Mailbox string `json:"mailbox,omitempty"`
	Folder  string `json:"folder,omitempty"`
}

func parsePSTParams(raw string) (PSTExportParams, error) {
	p := PSTExportParams{Scope: "all"}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return p, nil
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return p, fmt.Errorf("invalid pst params: %w", err)
	}
	p.Scope = strings.ToLower(strings.TrimSpace(p.Scope))
	p.Mailbox = strings.TrimSpace(p.Mailbox)
	p.Folder = strings.TrimSpace(p.Folder)
	if p.Scope == "" {
		p.Scope = "all"
	}
	switch p.Scope {
	case "all":
		p.Mailbox, p.Folder = "", ""
	case "mailbox":
		if p.Mailbox == "" {
			return p, fmt.Errorf("mailbox required for scope=mailbox")
		}
		p.Folder = ""
	case "folder":
		if p.Mailbox == "" || p.Folder == "" {
			return p, fmt.Errorf("mailbox and folder required for scope=folder")
		}
	default:
		return p, fmt.Errorf("unknown pst scope %q (use all, mailbox, or folder)", p.Scope)
	}
	return p, nil
}

// EncodePSTParams marshals export scope for job.Params.
func EncodePSTParams(scope, mailbox, folder string) (string, error) {
	p := PSTExportParams{
		Scope:   strings.ToLower(strings.TrimSpace(scope)),
		Mailbox: strings.TrimSpace(mailbox),
		Folder:  strings.TrimSpace(folder),
	}
	if p.Scope == "" {
		p.Scope = "all"
	}
	cleaned, err := parsePSTParamsMust(p)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(cleaned)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parsePSTParamsMust(p PSTExportParams) (PSTExportParams, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return p, err
	}
	return parsePSTParams(string(b))
}

// PSTExport builds mailbox archives from the live Exchange sync tree into
// {repo}/exports/pst/{runID}/. Output is a ZIP of .eml files per user (Outlook /
// Thunderbird can import EMLs). True binary .pst is not produced (no OSS writer).
//
// Job.Params may restrict the run to one mailbox or one folder (JSON PSTExportParams).
type PSTExport struct{}

func (PSTExport) Name() string { return "pst" }

func (PSTExport) Run(ctx context.Context, _ *graph.Client, tenant *db.Tenant, job *db.Job, _ string, _ TokenStore, cat *catalog.Store) (Result, error) {
	res := NewResult(ctx)
	res.SkipSnapshot = true

	params, err := parsePSTParams(job.Params)
	if err != nil {
		return res, err
	}
	if cat == nil {
		return res, fmt.Errorf("kein Katalog — zuerst Exchange-Backup ausführen")
	}
	has, err := cat.HasLiveItems(ctx, "exchange")
	if err != nil {
		return res, err
	}
	if !has {
		return res, fmt.Errorf("kein Exchange-Katalog vorhanden — zuerst Exchange-Backup ausführen")
	}

	targets, err := resolvePSTTargets(ctx, cat, params)
	if err != nil {
		return res, err
	}

	runID, runDir, err := storage.EnsurePSTExportDir(tenant.StorePath)
	if err != nil {
		return res, err
	}
	res.ExportPath = runDir
	res.Info(fmt.Sprintf("PST-Export (%s) nach %s", pstScopeLabel(params), runDir))
	res.addTotal(len(targets))

	var totalBytes int64
	var totalFiles int
	for i, t := range targets {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		zipPath := filepath.Join(runDir, t.ZipName)
		pct := 5 + (i*90)/len(targets)
		msg := fmt.Sprintf("Exportiere %s (%d/%d)…", t.Label, i+1, len(targets))
		res.Info(msg)
		if p := ProgressFrom(ctx); p != nil {
			p.SyncJob(job, &res, pct, msg)
		}

		nFiles, nBytes, err := cat.ZipItems(ctx, "exchange", t.Mailbox, t.Folder, zipPath)
		if err != nil {
			res.Warn(fmt.Sprintf("%s: %v", t.Label, err))
			continue
		}
		if nFiles == 0 {
			_ = os.Remove(zipPath)
			res.Skip(t.Label)
			continue
		}
		totalFiles += nFiles
		totalBytes += nBytes
		res.addItems(1, nBytes)
		res.Info(fmt.Sprintf("%s → %s (%d Mails, %s)", t.Label, t.ZipName, nFiles, storage.FormatBytes(nBytes)))
	}

	manifest := storage.PSTExportRun{
		ID:        runID,
		CreatedAt: time.Now().UTC(),
		Path:      runDir,
		Users:     res.ItemsNew,
		Files:     totalFiles,
		Bytes:     totalBytes,
		Scope:     params.Scope,
		Mailbox:   params.Mailbox,
		Folder:    params.Folder,
	}
	if err := storage.WritePSTManifest(runDir, manifest); err != nil {
		res.Warn("manifest: " + err.Error())
	}

	if res.ItemsNew == 0 {
		_ = os.RemoveAll(runDir)
		return res, fmt.Errorf("keine Mails zum Exportieren gefunden")
	}

	res.Info(fmt.Sprintf("Fertig: %d Archiv(e), %d Dateien, %s → exports/pst/%s",
		res.ItemsNew, totalFiles, storage.FormatBytes(totalBytes), runID))
	return res, nil
}

type pstTarget struct {
	Label   string
	Mailbox string
	Folder  string
	ZipName string
}

func resolvePSTTargets(ctx context.Context, cat *catalog.Store, params PSTExportParams) ([]pstTarget, error) {
	switch params.Scope {
	case "mailbox":
		boxes, err := cat.ListMailboxes(ctx, "exchange")
		if err != nil {
			return nil, err
		}
		if !containsStr(boxes, params.Mailbox) {
			return nil, fmt.Errorf("Postfach nicht gefunden: %s", params.Mailbox)
		}
		return []pstTarget{{
			Label:   params.Mailbox,
			Mailbox: params.Mailbox,
			ZipName: storage.SanitizeExportName(params.Mailbox) + ".zip",
		}}, nil
	case "folder":
		folders, err := cat.ListFolders(ctx, "exchange", params.Mailbox)
		if err != nil {
			return nil, err
		}
		if !containsStr(folders, params.Folder) {
			return nil, fmt.Errorf("Ordner nicht gefunden: %s", params.Folder)
		}
		return []pstTarget{{
			Label:   params.Mailbox + " / " + params.Folder,
			Mailbox: params.Mailbox,
			Folder:  params.Folder,
			ZipName: storage.SanitizeExportName(params.Mailbox) + "__" + storage.SanitizeExportName(params.Folder) + ".zip",
		}}, nil
	default:
		mailboxes, err := cat.ListMailboxes(ctx, "exchange")
		if err != nil {
			return nil, err
		}
		if len(mailboxes) == 0 {
			return nil, fmt.Errorf("Exchange-Katalog enthält keine Postfächer")
		}
		out := make([]pstTarget, 0, len(mailboxes))
		for _, m := range mailboxes {
			out = append(out, pstTarget{
				Label:   m,
				Mailbox: m,
				ZipName: storage.SanitizeExportName(m) + ".zip",
			})
		}
		return out, nil
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func pstScopeLabel(p PSTExportParams) string {
	switch p.Scope {
	case "mailbox":
		return "Postfach " + p.Mailbox
	case "folder":
		return "Ordner " + p.Mailbox + "/" + p.Folder
	default:
		return "alle Postfächer"
	}
}

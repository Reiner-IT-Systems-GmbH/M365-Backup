package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
)

type SharePointBackup struct{}

func (SharePointBackup) Name() string { return "sharepoint" }

func (SharePointBackup) Run(ctx context.Context, gc *graph.Client, tenant *db.Tenant, job *db.Job, stageDir string, tokens TokenStore) (Result, error) {
	res := NewResult(ctx)
	base := filepath.Join(stageDir, "sharepoint")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return res, err
	}

	res.Info("listing SharePoint sites…")
	sites, err := gc.Graph.Sites().Get(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("list sites: %w", err)
	}
	for _, site := range sites.GetValue() {
		sid := ptrStr(site.GetId())
		name := sanitize(ptrStr(site.GetDisplayName()))
		if sid == "" {
			continue
		}
		res.ItemsTotal++
		siteDir := filepath.Join(base, name)
		if err := os.MkdirAll(siteDir, 0o755); err != nil {
			res.Warn(err.Error())
			continue
		}
		meta, _ := json.MarshalIndent(map[string]string{
			"id": sid, "name": ptrStr(site.GetDisplayName()), "webUrl": ptrStr(site.GetWebUrl()),
		}, "", "  ")
		_ = os.WriteFile(filepath.Join(siteDir, "site.json"), meta, 0o600)

		drive, err := gc.Graph.Sites().BySiteId(sid).Drive().Get(ctx, nil)
		if err != nil {
			res.Warn(name+": "+err.Error())
			_ = tokens.UpsertDeltaToken(ctx, db.DeltaToken{
				TenantID: tenant.ID, Service: "sharepoint", UserID: sid, Token: "sync-" + job.ID,
			})
			continue
		}
		driveID := ptrStr(drive.GetId())
		children, err := gc.Graph.Drives().ByDriveId(driveID).Items().ByDriveItemId("root").Children().Get(ctx, nil)
		if err != nil {
			res.Warn(name+" children: "+err.Error())
			continue
		}
		for _, item := range children.GetValue() {
			if item.GetFile() == nil {
				continue
			}
			fname := sanitize(ptrStr(item.GetName()))
			itemID := ptrStr(item.GetId())
			contentURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/drives/%s/items/%s/content", driveID, itemID)
			data, err := gc.GetBytes(ctx, contentURL)
			if err != nil {
				res.Warn(fname+": "+err.Error())
				continue
			}
			if err := os.WriteFile(filepath.Join(siteDir, fname), data, 0o600); err != nil {
				res.Warn(err.Error())
				continue
			}
			res.ItemsNew++
			res.BytesTransferred += int64(len(data))
		}
		_ = tokens.UpsertDeltaToken(ctx, db.DeltaToken{
			TenantID: tenant.ID, Service: "sharepoint", UserID: sid, Token: "sync-" + job.ID,
		})
	}
	return res, nil
}

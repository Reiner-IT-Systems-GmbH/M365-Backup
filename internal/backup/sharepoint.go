package backup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rhw/m365backup/internal/catalog"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
)

type SharePointBackup struct{}

func (SharePointBackup) Name() string { return "sharepoint" }

func (SharePointBackup) Run(ctx context.Context, gc *graph.Client, tenant *db.Tenant, job *db.Job, stageDir string, tokens TokenStore, cat *catalog.Store) (Result, error) {
	_ = stageDir
	res := NewResult(ctx)
	cat.StartReconcile("sharepoint")

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
		meta, _ := json.MarshalIndent(map[string]string{
			"id": sid, "name": ptrStr(site.GetDisplayName()), "webUrl": ptrStr(site.GetWebUrl()),
		}, "", "  ")
		if err := cat.Put(ctx, catalog.Item{
			Service: "sharepoint", GraphItemID: "sp:" + sid + ":meta", Mailbox: name,
			Name: "site.json", ContentType: "application/json",
		}, meta); err != nil {
			res.Warn(err.Error())
		}

		drive, err := gc.Graph.Sites().BySiteId(sid).Drive().Get(ctx, nil)
		if err != nil {
			res.Warn(name + ": " + err.Error())
			_ = tokens.UpsertDeltaToken(ctx, db.DeltaToken{
				TenantID: tenant.ID, Service: "sharepoint", UserID: sid, Token: "sync-" + job.ID,
			})
			continue
		}
		driveID := ptrStr(drive.GetId())
		children, err := gc.Graph.Drives().ByDriveId(driveID).Items().ByDriveItemId("root").Children().Get(ctx, nil)
		if err != nil {
			res.Warn(name + " children: " + err.Error())
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
				res.Warn(fname + ": " + err.Error())
				continue
			}
			gid := itemID
			if gid == "" {
				gid = "sp:" + sid + ":file:" + fname
			} else {
				gid = "sp:" + sid + ":" + itemID
			}
			if err := cat.Put(ctx, catalog.Item{
				Service: "sharepoint", GraphItemID: gid, Mailbox: name,
				Name: fname,
			}, data); err != nil {
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
	if n, err := cat.FinishReconcile(ctx, "sharepoint"); err != nil {
		res.Warn("reconcile: " + err.Error())
	} else if n > 0 {
		res.Info(fmt.Sprintf("reconcile: marked %d unseen SharePoint items deleted", n))
	}
	return res, nil
}

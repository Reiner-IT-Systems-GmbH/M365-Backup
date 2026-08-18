package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/rhw/m365backup/internal/catalog"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
)

type TeamsBackup struct{}

func (TeamsBackup) Name() string { return "teams" }

func (TeamsBackup) Run(ctx context.Context, gc *graph.Client, tenant *db.Tenant, job *db.Job, stageDir string, tokens TokenStore, cat *catalog.Store) (Result, error) {
	_ = stageDir
	res := NewResult(ctx)
	cat.StartReconcile("teams")

	res.Info("listing Teams…")
	teams, err := gc.Graph.Teams().Get(ctx, nil)
	if err != nil {
		return res, fmt.Errorf("list teams: %w", err)
	}
	for _, team := range teams.GetValue() {
		tid := ptrStr(team.GetId())
		tname := sanitize(ptrStr(team.GetDisplayName()))
		if tid == "" {
			continue
		}
		res.ItemsTotal++

		channels, err := gc.Graph.Teams().ByTeamId(tid).Channels().Get(ctx, nil)
		if err != nil {
			res.Warn(tname + ": " + err.Error())
			continue
		}
		for _, ch := range channels.GetValue() {
			cid := ptrStr(ch.GetId())
			cname := sanitize(ptrStr(ch.GetDisplayName()))
			msgs, err := gc.Graph.Teams().ByTeamId(tid).Channels().ByChannelId(cid).Messages().Get(ctx, nil)
			if err != nil {
				res.Warn(cname + ": " + err.Error())
				continue
			}
			var archive []map[string]any
			var htmlParts []string
			htmlParts = append(htmlParts, "<!DOCTYPE html><html><head><meta charset=\"utf-8\"><title>"+html.EscapeString(cname)+"</title></head><body>")
			htmlParts = append(htmlParts, "<h1>"+html.EscapeString(tname)+" / "+html.EscapeString(cname)+"</h1>")

			for _, msg := range msgs.GetValue() {
				mid := ptrStr(msg.GetId())
				body := ""
				if msg.GetBody() != nil && msg.GetBody().GetContent() != nil {
					body = *msg.GetBody().GetContent()
				}
				from := ""
				if msg.GetFrom() != nil && msg.GetFrom().GetUser() != nil {
					from = ptrStr(msg.GetFrom().GetUser().GetDisplayName())
				}
				created := ""
				if msg.GetCreatedDateTime() != nil {
					created = msg.GetCreatedDateTime().Format(time.RFC3339)
				}
				entry := map[string]any{"id": mid, "from": from, "created": created, "body": body}
				archive = append(archive, entry)
				htmlParts = append(htmlParts, fmt.Sprintf("<article><header><strong>%s</strong> <time>%s</time></header><div>%s</div></article>",
					html.EscapeString(from), html.EscapeString(created), body))
				res.ItemsNew++
				res.BytesTransferred += int64(len(body))
			}
			htmlParts = append(htmlParts, "</body></html>")
			jb, _ := json.MarshalIndent(archive, "", "  ")
			mailbox := tname
			parent := cname
			jsonID := "teams:" + tid + ":" + cid + ":json"
			htmlID := "teams:" + tid + ":" + cid + ":html"
			if err := cat.Put(ctx, catalog.Item{
				Service: "teams", GraphItemID: jsonID, Mailbox: mailbox, ParentPath: parent,
				Name: "messages.json", ContentType: "application/json",
			}, jb); err != nil {
				res.Warn(err.Error())
			}
			if err := cat.Put(ctx, catalog.Item{
				Service: "teams", GraphItemID: htmlID, Mailbox: mailbox, ParentPath: parent,
				Name: "messages.html", ContentType: "text/html",
			}, []byte(strings.Join(htmlParts, "\n"))); err != nil {
				res.Warn(err.Error())
			}

			_ = tokens.UpsertDeltaToken(ctx, db.DeltaToken{
				TenantID: tenant.ID, Service: "teams", UserID: tid + ":" + cid, Token: "sync-" + job.ID,
			})
		}
	}
	if n, err := cat.FinishReconcile(ctx, "teams"); err != nil {
		res.Warn("reconcile: " + err.Error())
	} else if n > 0 {
		res.Info(fmt.Sprintf("reconcile: marked %d unseen Teams items deleted", n))
	}
	return res, nil
}

package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	abstractions "github.com/microsoft/kiota-abstractions-go"
	"github.com/microsoftgraph/msgraph-sdk-go/drives"
	"github.com/microsoftgraph/msgraph-sdk-go/models"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
)

// OneDriveBackup syncs personal drives into a persistent tree with Graph delta.
// Empty drives and empty folders are not created / not logged individually.
type OneDriveBackup struct {
	Workers int
}

func (OneDriveBackup) Name() string { return "onedrive" }

func (o OneDriveBackup) workers() int {
	if o.Workers < 1 {
		return 6
	}
	return o.Workers
}

func (o OneDriveBackup) Run(ctx context.Context, gc *graph.Client, tenant *db.Tenant, job *db.Job, stageDir string, tokens TokenStore) (Result, error) {
	res := NewResult(ctx)
	prog := ProgressFrom(ctx)
	workers := o.workers()

	res.Info("listing users for OneDrive…")
	prog.SyncJob(job, &res, 3, "Listing users for OneDrive…")
	users, err := gc.ListUsers(ctx)
	if err != nil {
		return res, fmt.Errorf("list users: %w", err)
	}

	syncBase := filepath.Join(tenant.KopiaRepoPath, "sync", "onedrive")
	if err := os.MkdirAll(syncBase, 0o755); err != nil {
		return res, err
	}
	res.SnapshotDir = syncBase

	total := len(users)
	res.Info(fmt.Sprintf("listed %d directory objects; OneDrive sync with %d workers (delta after first run; empty drives skipped)",
		total, workers))
	prog.SyncJob(job, &res, 5, fmt.Sprintf("Listed %d users — %d workers…", total, workers))

	type item struct {
		idx int
		u   models.Userable
	}
	ch := make(chan item)
	var (
		wg      sync.WaitGroup
		okCount atomic.Int64
		doneN   atomic.Int64
	)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := range ch {
				if ctx.Err() != nil {
					return
				}
				o.backupOneDrive(ctx, gc, tenant, job, tokens, syncBase, it.idx, total, it.u, &res, prog, &okCount, &doneN)
			}
		}()
	}
	for i, u := range users {
		select {
		case <-ctx.Done():
			close(ch)
			wg.Wait()
			return res, ctx.Err()
		case ch <- item{idx: i, u: u}:
		}
	}
	close(ch)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return res, err
	}

	itemsNew, itemsTotal, skipped, bytes := res.snapshot()
	done := fmt.Sprintf("done: %d file change(s), %d drives with data, %d skipped/empty, %d considered, %d bytes",
		itemsNew, okCount.Load(), skipped, itemsTotal, bytes)
	res.Info(done)
	prog.SyncJob(job, &res, 92, done)
	_ = os.WriteFile(filepath.Join(syncBase, "BACKUP_META.txt"), []byte(done+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(stageDir, "SNAPSHOT_ROOT.txt"), []byte(syncBase+"\n"), 0o600)
	return res, nil
}

func (o OneDriveBackup) backupOneDrive(
	ctx context.Context, gc *graph.Client, tenant *db.Tenant, job *db.Job, tokens TokenStore,
	syncBase string, idx, total int, u models.Userable, res *Result, prog *Progress,
	okCount, doneN *atomic.Int64,
) {
	uid := ptrStr(u.GetId())
	upn := ptrStr(u.GetUserPrincipalName())
	if uid == "" {
		return
	}
	if !strings.Contains(upn, "@") && ptrStr(u.GetMail()) == "" {
		res.Skip("")
		return
	}
	res.addTotal(1)

	pctFor := func() int {
		n := int(doneN.Load())
		if total <= 0 {
			return 50
		}
		return 5 + (n*85)/total
	}

	drive, err := gc.Graph.Users().ByUserId(uid).Drive().Get(ctx, nil)
	if err != nil {
		if isDriveUnavailable(err) {
			res.Skip("")
			doneN.Add(1)
			return
		}
		res.Warn(fmt.Sprintf("%s drive: %v", upn, err))
		doneN.Add(1)
		return
	}
	driveID := ptrStr(drive.GetId())
	if driveID == "" {
		res.Skip("")
		doneN.Add(1)
		return
	}

	saved, _ := tokens.GetDeltaToken(ctx, tenant.ID, "onedrive", uid)
	if strings.HasPrefix(saved, "sync-") {
		saved = "" // old placeholder
	}
	mode := "incremental delta"
	if saved == "" {
		mode = "FULL initial sync"
	}
	prog.SyncJob(job, res, pctFor(), fmt.Sprintf("[%d/%d] OneDrive %s (%s)…", idx+1, total, upn, mode))

	userDir := filepath.Join(syncBase, sanitize(upn))
	n, warn := syncDriveDelta(ctx, gc, tokens, tenant.ID, uid, upn, driveID, userDir, saved, res)
	for _, w := range warn {
		res.Warn(w)
	}

	if n == 0 && !dirHasFiles(userDir) {
		_ = os.RemoveAll(userDir)
		res.Skip("")
		doneN.Add(1)
		return
	}
	okCount.Add(1)
	finished := doneN.Add(1)
	itemsNew, _, _, bytes := res.snapshot()
	res.Info(fmt.Sprintf("[%d/%d] %s: %d file change(s) · job total %d files / %d bytes (drives done %d/%d)",
		idx+1, total, upn, n, itemsNew, bytes, finished, total))
	prog.SyncJob(job, res, pctFor(), fmt.Sprintf("[%d/%d] %s done", idx+1, total, upn))
}

func syncDriveDelta(
	ctx context.Context, gc *graph.Client, tokens TokenStore, tenantID, userID, upn, driveID, userDir, saved string, res *Result,
) (int, []string) {
	var warnings []string
	hdr := abstractions.NewRequestHeaders()
	hdr.Add("Prefer", "odata.maxpagesize=200")
	cfg := &drives.ItemItemsItemDeltaRequestBuilderGetRequestConfiguration{
		Headers: hdr,
		QueryParameters: &drives.ItemItemsItemDeltaRequestBuilderGetQueryParameters{
			Select: []string{"id", "name", "file", "folder", "parentReference", "deleted", "size"},
		},
	}

	deltaRB := gc.Graph.Drives().ByDriveId(driveID).Items().ByDriveItemId("root").Delta()
	var (
		resp drives.ItemItemsItemDeltaGetResponseable
		err  error
	)
	if saved != "" {
		resp, err = deltaRB.WithUrl(saved).GetAsDeltaGetResponse(ctx, nil)
		if err != nil && isDeltaGone(err) {
			res.Info(fmt.Sprintf("%s: OneDrive delta expired — full resync", upn))
			saved = ""
			err = nil
		}
	}
	if saved == "" && err == nil {
		resp, err = deltaRB.GetAsDeltaGetResponse(ctx, cfg)
	}
	if err != nil {
		if isDriveUnavailable(err) {
			return 0, nil
		}
		return 0, []string{fmt.Sprintf("%s delta: %v", upn, err)}
	}

	n := 0
	for {
		if err := ctx.Err(); err != nil {
			return n, append(warnings, err.Error())
		}
		for _, item := range resp.GetValue() {
			if err := ctx.Err(); err != nil {
				return n, append(warnings, err.Error())
			}
			rel := driveItemRelPath(item)
			if rel == "" || rel == "." {
				continue
			}
			abs := filepath.Join(userDir, rel)
			if !underRoot(userDir, abs) {
				warnings = append(warnings, fmt.Sprintf("%s: rejected path %q", upn, rel))
				continue
			}

			if isDriveItemRemoved(item) {
				_ = os.RemoveAll(abs)
				pruneEmptyDirs(filepath.Dir(abs), userDir)
				n++
				res.addItems(1, 0)
				continue
			}
			// Folders: do not create empty dirs — only materialize when a file needs them.
			if item.GetFolder() != nil && item.GetFile() == nil {
				continue
			}
			if item.GetFile() == nil {
				continue
			}
			itemID := ptrStr(item.GetId())
			if itemID == "" {
				continue
			}
			contentURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/drives/%s/items/%s/content", driveID, itemID)
			data, err := gc.GetBytes(ctx, contentURL)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s %s: %v", upn, rel, err))
				continue
			}
			if err := ensureParent(abs); err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			if err := os.WriteFile(abs, data, 0o600); err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			res.addItems(1, int64(len(data)))
			n++
			if n%100 == 0 {
				res.Info(fmt.Sprintf("%s: progress %d file change(s) (not a limit)…", upn, n))
			}
		}
		next := resp.GetOdataNextLink()
		if next != nil && *next != "" {
			resp, err = deltaRB.WithUrl(*next).GetAsDeltaGetResponse(ctx, nil)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s page: %v", upn, err))
				break
			}
			continue
		}
		delta := resp.GetOdataDeltaLink()
		if delta != nil && *delta != "" {
			_ = tokens.UpsertDeltaToken(ctx, db.DeltaToken{
				TenantID: tenantID, Service: "onedrive", UserID: userID, Token: *delta,
			})
		}
		break
	}
	return n, warnings
}

func driveItemRelPath(item models.DriveItemable) string {
	name := sanitize(ptrStr(item.GetName()))
	if name == "" || strings.EqualFold(name, "root") {
		name = ""
	}
	var parts []string
	if pr := item.GetParentReference(); pr != nil {
		p := ptrStr(pr.GetPath())
		if i := strings.Index(p, "/root:"); i >= 0 {
			p = p[i+len("/root:"):]
		}
		p = strings.Trim(p, "/")
		for _, seg := range strings.Split(p, "/") {
			if seg == "" {
				continue
			}
			parts = append(parts, sanitize(seg))
		}
	}
	if name != "" {
		parts = append(parts, name)
	}
	if len(parts) == 0 {
		return ""
	}
	return filepath.Join(parts...)
}

func isDriveItemRemoved(item models.DriveItemable) bool {
	if item.GetDeleted() != nil {
		return true
	}
	ad := item.GetAdditionalData()
	if ad == nil {
		return false
	}
	_, ok := ad["@removed"]
	return ok
}

func isDriveUnavailable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	needles := []string{
		"nospecialcharacters", "mailboxnotenabled", "resourcenotfound",
		"itemnotfound", "accessdenied", "notallowed", "quota", "license",
		"user has no drive", "does not have a drive", "mysite", "spo_error",
		"invalidrequest", "badrequest", "forbidden",
	}
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	abstractions "github.com/microsoft/kiota-abstractions-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/users"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/graph"
	"github.com/rhw/m365backup/internal/storage"
)

// ExchangeBackup backs up Exchange Online mailboxes.
// First run does a full Graph delta sync into a persistent tree under the tenant repo;
// later runs only fetch changes (deltaLink). Multiple mailboxes run in parallel.
type ExchangeBackup struct {
	Workers int // parallel mailboxes; default 6
}

func (ExchangeBackup) Name() string { return "exchange" }

func (e ExchangeBackup) workers() int {
	if e.Workers < 1 {
		return 6
	}
	return e.Workers
}

func (e ExchangeBackup) Run(ctx context.Context, gc *graph.Client, tenant *db.Tenant, job *db.Job, stageDir string, tokens TokenStore) (Result, error) {
	res := NewResult(ctx)
	prog := ProgressFrom(ctx)
	workers := e.workers()

	res.Info("listing users & shared mailboxes from Graph…")
	prog.SyncJob(job, &res, 3, "Listing users & shared mailboxes…")
	userList, err := gc.ListUsers(ctx)
	if err != nil {
		return res, fmt.Errorf("list users: %w", err)
	}

	// Persistent sync tree (survives jobs) so incremental delta can update in place.
	syncBase := filepath.Join(tenant.KopiaRepoPath, "sync", "exchange")
	if err := os.MkdirAll(syncBase, 0o755); err != nil {
		return res, err
	}
	res.SnapshotDir = syncBase

	res.Info(fmt.Sprintf("listed %d directory objects; syncing with %d parallel mailbox workers (Graph delta = incremental after first run)",
		len(userList), workers))
	prog.SyncJob(job, &res, 5, fmt.Sprintf("Listed %d objects — %d workers…", len(userList), workers))

	type item struct {
		idx int
		u   models.Userable
	}
	jobs := make(chan item)
	var (
		wg       sync.WaitGroup
		okCount  atomic.Int64
		sharedOK atomic.Int64
		doneN    atomic.Int64
	)
	total := len(userList)

	workerFn := func() {
		defer wg.Done()
		for it := range jobs {
			if err := ctx.Err(); err != nil {
				return
			}
			e.backupOneMailbox(ctx, gc, tenant, job, tokens, syncBase, it.idx, total, it.u, &res, prog, &okCount, &sharedOK, &doneN)
		}
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go workerFn()
	}
	for i, u := range userList {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return res, ctx.Err()
		case jobs <- item{idx: i, u: u}:
		}
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return res, err
	}

	itemsNew, itemsTotal, skipped, bytes := res.snapshot()
	done := fmt.Sprintf("done: %d messages touched, %d mailboxes ok (%d shared), %d skipped, %d considered, %d bytes (persistent sync + delta)",
		itemsNew, okCount.Load(), sharedOK.Load(), skipped, itemsTotal, bytes)
	res.Info(done)
	prog.SyncJob(job, &res, 92, done)
	meta := fmt.Sprintf("tenant=%s service=exchange users=%d mailboxes_ok=%d shared_ok=%d skipped=%d emails=%d workers=%d\n",
		tenant.ID, len(userList), okCount.Load(), sharedOK.Load(), skipped, itemsNew, workers)
	_ = os.WriteFile(filepath.Join(syncBase, "BACKUP_META.txt"), []byte(meta), 0o600)
	// Keep stageDir marker so operators see where data lives
	_ = os.WriteFile(filepath.Join(stageDir, "SNAPSHOT_ROOT.txt"), []byte(syncBase+"\n"), 0o600)
	return res, nil
}

func (e ExchangeBackup) backupOneMailbox(
	ctx context.Context, gc *graph.Client, tenant *db.Tenant, job *db.Job, tokens TokenStore,
	syncBase string, idx, total int, u models.Userable, res *Result, prog *Progress,
	okCount, sharedOK, doneN *atomic.Int64,
) {
	uid := ptrStr(u.GetId())
	upn := ptrStr(u.GetUserPrincipalName())
	if uid == "" {
		return
	}
	mail := ptrStr(u.GetMail())
	if mail == "" && !strings.Contains(upn, "@") {
		res.Skip("")
		return
	}
	res.addTotal(1)

	kind := "user"
	if u.GetAccountEnabled() != nil && !*u.GetAccountEnabled() {
		kind = "shared"
	}

	pctFor := func() int {
		n := int(doneN.Load())
		if total <= 0 {
			return 50
		}
		return 5 + (n*85)/total
	}

	msg := fmt.Sprintf("[%d/%d] probing mailbox %s…", idx+1, total, upn)
	prog.SyncJob(job, res, pctFor(), msg)

	folders, err := listAllMailFolders(ctx, gc, uid)
	if err != nil {
		if isMailboxUnavailable(err) {
			res.Skip("")
			doneN.Add(1)
			return
		}
		res.Warn(fmt.Sprintf("%s: list folders: %v", upn, err))
		doneN.Add(1)
		return
	}

	modeHint := "incremental delta"
	// Heuristic: if no folder has a real delta token yet, this mailbox is still initial sync.
	anyDelta := false
	for _, folder := range folders {
		fid := ptrStr(folder.GetId())
		tok, _ := tokens.GetDeltaToken(ctx, tenant.ID, "exchange", deltaKey(uid, fid))
		if tok != "" && !strings.HasPrefix(tok, "full-") {
			anyDelta = true
			break
		}
	}
	if !anyDelta {
		modeHint = "FULL initial sync"
	}

	msg = fmt.Sprintf("[%d/%d] %s [%s]: %d folder(s) — %s…", idx+1, total, upn, kind, len(folders), modeHint)
	res.Info(msg)
	prog.SyncJob(job, res, pctFor(), msg)

	userDir := filepath.Join(syncBase, sanitize(upn))
	nMsgs := 0
	for _, folder := range folders {
		if err := ctx.Err(); err != nil {
			return
		}
		fid := ptrStr(folder.GetId())
		fname := sanitize(ptrStr(folder.GetDisplayName()))
		if fid == "" {
			continue
		}
		if fname == "" {
			fname = sanitize(fid)
		}
		folderDir := filepath.Join(userDir, fname)
		// Do not pre-create empty folders — only when messages are written.
		n, warn := backupFolderDelta(ctx, gc, tokens, tenant.ID, uid, upn, fid, fname, folderDir, userDir, res)
		nMsgs += n
		for _, w := range warn {
			res.Warn(w)
		}
	}

	finished := doneN.Add(1)
	if nMsgs == 0 && !dirHasFiles(userDir) {
		_ = os.RemoveAll(userDir)
		prog.SyncJob(job, res, pctFor(), fmt.Sprintf("[%d/%d] %s — no messages (no tree)", idx+1, total, upn))
		_ = finished
		return
	}
	okCount.Add(1)
	if kind == "shared" {
		sharedOK.Add(1)
	}
	itemsNew, _, _, bytes := res.snapshot()
	doneMsg := fmt.Sprintf("[%d/%d] %s [%s]: %d message change(s) this run · job total %d msgs / %d bytes (mailboxes done %d/%d)",
		idx+1, total, upn, kind, nMsgs, itemsNew, bytes, finished, total)
	res.Info(doneMsg)
	prog.SyncJob(job, res, pctFor(), doneMsg)
}

func deltaKey(userID, folderID string) string {
	return userID + "|" + folderID
}

func backupFolderDelta(
	ctx context.Context, gc *graph.Client, tokens TokenStore, tenantID, userID, upn, folderID, folderName, folderDir, userDir string, res *Result,
) (int, []string) {
	var warnings []string
	tokenKey := deltaKey(userID, folderID)
	saved, _ := tokens.GetDeltaToken(ctx, tenantID, "exchange", tokenKey)
	if strings.HasPrefix(saved, "full-") {
		saved = "" // old placeholder from pre-delta builds
	}

	hdr := abstractions.NewRequestHeaders()
	hdr.Add("Prefer", "odata.maxpagesize=100")
	cfg := &users.ItemMailFoldersItemMessagesDeltaRequestBuilderGetRequestConfiguration{
		Headers: hdr,
		QueryParameters: &users.ItemMailFoldersItemMessagesDeltaRequestBuilderGetQueryParameters{
			Select: []string{"id", "subject"},
		},
	}

	var (
		resp users.ItemMailFoldersItemMessagesDeltaGetResponseable
		err  error
	)
	deltaRB := gc.Graph.Users().ByUserId(userID).MailFolders().ByMailFolderId(folderID).Messages().Delta()
	if saved != "" {
		resp, err = deltaRB.WithUrl(saved).GetAsDeltaGetResponse(ctx, nil)
		if err != nil && isDeltaGone(err) {
			res.Info(fmt.Sprintf("%s / %s: delta token expired — full resync of folder", upn, folderName))
			saved = ""
			err = nil
		}
	}
	if saved == "" && err == nil {
		resp, err = deltaRB.GetAsDeltaGetResponse(ctx, cfg)
	}
	if err != nil {
		if isMailboxUnavailable(err) {
			return 0, nil
		}
		return 0, []string{fmt.Sprintf("%s folder %s delta: %v", upn, folderName, err)}
	}

	n := 0
	for {
		if err := ctx.Err(); err != nil {
			return n, append(warnings, err.Error())
		}
		for _, m := range resp.GetValue() {
			if err := ctx.Err(); err != nil {
				return n, append(warnings, err.Error())
			}
			mid := ptrStr(m.GetId())
			if mid == "" {
				continue
			}
			subj := ptrStr(m.GetSubject())
			path := filepath.Join(folderDir, emlFileName(subj, mid))
			if isGraphRemoved(m) {
				removeEMLVariants(folderDir, mid)
				pruneEmptyDirs(folderDir, userDir)
				n++
				res.addItems(1, 0)
				continue
			}
			rawURL := fmt.Sprintf("https://graph.microsoft.com/v1.0/users/%s/messages/%s/$value", userID, mid)
			body, err := gc.GetBytes(ctx, rawURL)
			if err != nil {
				body = []byte("Subject: " + subj + "\r\n\r\n[MIME fetch failed: " + err.Error() + "]\r\n")
				warnings = append(warnings, fmt.Sprintf("%s mime %s: %v", upn, mid, err))
			}
			// Drop legacy Graph-ID filenames when rewriting.
			_ = os.Remove(filepath.Join(folderDir, sanitize(mid)+".eml"))
			if err := ensureParent(path); err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			res.addItems(1, int64(len(body)))
			n++
			if n%250 == 0 {
				// Progress heartbeat only — NOT a download limit; pagination continues.
				res.Info(fmt.Sprintf("%s / %s: progress %d message change(s) this folder (not a limit)…", upn, folderName, n))
			}
		}
		next := resp.GetOdataNextLink()
		if next != nil && *next != "" {
			resp, err = deltaRB.WithUrl(*next).GetAsDeltaGetResponse(ctx, nil)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s folder page: %v", upn, err))
				break
			}
			continue
		}
		delta := resp.GetOdataDeltaLink()
		if delta != nil && *delta != "" {
			_ = tokens.UpsertDeltaToken(ctx, db.DeltaToken{
				TenantID: tenantID, Service: "exchange", UserID: tokenKey, Token: *delta,
			})
		}
		break
	}
	if n == 0 {
		pruneEmptyDirs(folderDir, userDir)
	}
	return n, warnings
}

func isGraphRemoved(m models.Messageable) bool {
	ad := m.GetAdditionalData()
	if ad == nil {
		return false
	}
	_, ok := ad["@removed"]
	return ok
}

func isDeltaGone(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "resyncrequired") ||
		strings.Contains(s, "syncstate") ||
		(strings.Contains(s, "410") && strings.Contains(s, "gone")) ||
		strings.Contains(s, "deltatoken")
}

func listAllMailFolders(ctx context.Context, gc *graph.Client, userID string) ([]models.MailFolderable, error) {
	top := int32(100)
	includeHidden := "true"
	cfg := &users.ItemMailFoldersRequestBuilderGetRequestConfiguration{
		QueryParameters: &users.ItemMailFoldersRequestBuilderGetQueryParameters{
			Top:                  &top,
			IncludeHiddenFolders: &includeHidden,
			Select:               []string{"id", "displayName", "parentFolderId", "childFolderCount"},
		},
	}
	resp, err := gc.Graph.Users().ByUserId(userID).MailFolders().Get(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var roots []models.MailFolderable
	for {
		roots = append(roots, resp.GetValue()...)
		next := resp.GetOdataNextLink()
		if next == nil || *next == "" {
			break
		}
		resp, err = gc.Graph.Users().ByUserId(userID).MailFolders().WithUrl(*next).Get(ctx, nil)
		if err != nil {
			return nil, err
		}
	}

	var all []models.MailFolderable
	for _, f := range roots {
		all = append(all, f)
		childs, err := listChildFolders(ctx, gc, userID, ptrStr(f.GetId()), 0)
		if err != nil {
			continue
		}
		all = append(all, childs...)
	}
	return all, nil
}

func listChildFolders(ctx context.Context, gc *graph.Client, userID, folderID string, depth int) ([]models.MailFolderable, error) {
	if folderID == "" || depth > 20 {
		return nil, nil
	}
	top := int32(100)
	cfg := &users.ItemMailFoldersItemChildFoldersRequestBuilderGetRequestConfiguration{
		QueryParameters: &users.ItemMailFoldersItemChildFoldersRequestBuilderGetQueryParameters{
			Top:    &top,
			Select: []string{"id", "displayName", "parentFolderId", "childFolderCount"},
		},
	}
	resp, err := gc.Graph.Users().ByUserId(userID).MailFolders().ByMailFolderId(folderID).ChildFolders().Get(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var out []models.MailFolderable
	for {
		for _, f := range resp.GetValue() {
			out = append(out, f)
			nested, _ := listChildFolders(ctx, gc, userID, ptrStr(f.GetId()), depth+1)
			out = append(out, nested...)
		}
		next := resp.GetOdataNextLink()
		if next == nil || *next == "" {
			break
		}
		resp, err = gc.Graph.Users().ByUserId(userID).MailFolders().ByMailFolderId(folderID).ChildFolders().WithUrl(*next).Get(ctx, nil)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func isMailboxUnavailable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	needles := []string{
		"inactive", "soft-deleted", "hosted on-premise", "hosted on-premises",
		"mailboxnotenabledforrestapi", "mailboxnotenabled", "erroritemnotfound",
		"the requested user", "mailbox is either", "resourcenotfound",
		"mailboxnotfound", "user is invalid", "invaliduserid",
	}
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func sanitize(s string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	s = r.Replace(s)
	s = strings.TrimSpace(s)
	// Reject path traversal / relative segments that filepath.Join would honor.
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	if strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", "_")
	}
	return s
}

// underRoot reports whether target is root or a path strictly inside root.
func underRoot(root, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if target == root {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(target, root+sep)
}

// emlFileName builds "Betreff hier__a1b2c3d4.eml" — readable + unique per Graph message id.
func emlFileName(subject, mid string) string {
	subj := sanitize(storage.DecodeMIMEHeader(subject))
	subj = strings.ReplaceAll(subj, "  ", " ")
	if subj == "" || subj == "_" {
		subj = "ohne-betreff"
	}
	runes := []rune(subj)
	if len(runes) > 80 {
		subj = string(runes[:80])
	}
	return fmt.Sprintf("%s__%s.eml", subj, msgShortID(mid))
}

func msgShortID(mid string) string {
	sum := sha256.Sum256([]byte(mid))
	return hex.EncodeToString(sum[:])[:10]
}

func removeEMLVariants(folderDir, mid string) {
	_ = os.Remove(filepath.Join(folderDir, sanitize(mid)+".eml"))
	short := msgShortID(mid)
	matches, _ := filepath.Glob(filepath.Join(folderDir, "*__"+short+".eml"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

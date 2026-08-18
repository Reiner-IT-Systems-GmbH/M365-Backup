package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"

	"github.com/rhw/m365backup/internal/blobstore"
	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/storage"
)

// Store is the per-tenant item catalog plus encrypted CAS.
type Store struct {
	DB       *db.DB
	Blobs    *blobstore.Store
	TenantID string
	Root     string
	Password string

	mu      sync.Mutex
	pending map[string]pendingChange // service\0graphID -> change
	seen    map[string]map[string]struct{}
}

func Open(database *db.DB, tenantID, root, password string) (*Store, error) {
	if _, err := storage.GuardPath(root); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "blobs"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "manifests"), 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "exports"), 0o700); err != nil {
		return nil, err
	}
	blobs, err := blobstore.New(root, password)
	if err != nil {
		return nil, err
	}
	return &Store{
		DB:       database,
		Blobs:    blobs,
		TenantID: tenantID,
		Root:     root,
		Password: password,
		pending:  map[string]pendingChange{},
		seen:     map[string]map[string]struct{}{},
	}, nil
}

func pendingKey(service, graphID string) string {
	return service + "\x00" + graphID
}

func (s *Store) notePending(op string, it Item) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[pendingKey(it.Service, it.GraphItemID)] = pendingChange{Op: op, Item: it}
	if s.seen[it.Service] != nil && op == OpUpsert {
		s.seen[it.Service][it.GraphItemID] = struct{}{}
	}
}

// StartReconcile records live item IDs seen this run so unseen ones can be marked deleted.
func (s *Store) StartReconcile(service string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen[service] = map[string]struct{}{}
}

func (s *Store) MarkSeen(service, graphID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[service] == nil {
		s.seen[service] = map[string]struct{}{}
	}
	s.seen[service][graphID] = struct{}{}
}

func (s *Store) Put(ctx context.Context, it Item, data []byte) error {
	if it.Service == "" || it.GraphItemID == "" {
		return fmt.Errorf("catalog put: service and graph_item_id required")
	}
	hash, err := s.Blobs.Put(data)
	if err != nil {
		return err
	}
	it.BlobHash = hash
	it.Size = int64(len(data))
	it.Deleted = false
	if it.MTime.IsZero() {
		it.MTime = time.Now().UTC()
	}
	if err := s.rematchID(ctx, &it); err != nil {
		return err
	}
	if err := s.upsertItem(ctx, it); err != nil {
		return err
	}
	s.notePending(OpUpsert, it)
	return nil
}

func (s *Store) Delete(ctx context.Context, service, graphItemID string) error {
	if service == "" || graphItemID == "" {
		return fmt.Errorf("catalog delete: service and graph_item_id required")
	}
	it := Item{Service: service, GraphItemID: graphItemID, Deleted: true}
	if err := s.rematchID(ctx, &it); err != nil {
		return err
	}
	existing, err := s.getItem(ctx, service, it.GraphItemID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if existing != nil {
		existing.Deleted = true
		it = *existing
		it.Deleted = true
	}
	if err := s.markDeleted(ctx, service, it.GraphItemID); err != nil {
		return err
	}
	s.notePending(OpDelete, it)
	return nil
}

// DeletePrefix marks all live items under mailbox/rel as deleted (folder remove).
func (s *Store) DeletePrefix(ctx context.Context, service, mailbox, rel string) error {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	rows, err := s.DB.SQL.QueryContext(ctx, `
		SELECT graph_item_id, mailbox, parent_path, name, blob_hash, size, mtime, content_type, subject, from_addr
		FROM catalog_items
		WHERE tenant_id=? AND service=? AND deleted=0 AND mailbox=?`,
		s.TenantID, service, mailbox)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []Item
	for rows.Next() {
		var it Item
		var mtime sql.NullTime
		if err := rows.Scan(&it.GraphItemID, &it.Mailbox, &it.ParentPath, &it.Name, &it.BlobHash, &it.Size, &mtime, &it.ContentType, &it.Subject, &it.FromAddr); err != nil {
			return err
		}
		if mtime.Valid {
			it.MTime = mtime.Time
		}
		it.Service = service
		p := it.RelPath()
		if rel == "" || p == mailbox+"/"+rel || strings.HasPrefix(p, mailbox+"/"+rel+"/") {
			ids = append(ids, it)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range ids {
		if err := s.Delete(ctx, service, it.GraphItemID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) FinishReconcile(ctx context.Context, service string) (int, error) {
	s.mu.Lock()
	seen := s.seen[service]
	s.mu.Unlock()
	if seen == nil {
		return 0, nil
	}
	rows, err := s.DB.SQL.QueryContext(ctx, `
		SELECT graph_item_id FROM catalog_items
		WHERE tenant_id=? AND service=? AND deleted=0`, s.TenantID, service)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var gone []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		if _, ok := seen[id]; !ok {
			gone = append(gone, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range gone {
		if err := s.Delete(ctx, service, id); err != nil {
			return 0, err
		}
	}
	return len(gone), nil
}

func (s *Store) CommitSnapshot(ctx context.Context, service, jobID string) (*Snapshot, error) {
	if service == "" {
		return nil, fmt.Errorf("commit snapshot: service required")
	}
	gen, err := s.nextGeneration(ctx, service)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	var changes []pendingChange
	for k, ch := range s.pending {
		if ch.Item.Service == service {
			changes = append(changes, ch)
			delete(s.pending, k)
		}
	}
	s.mu.Unlock()

	liveN, liveBytes, err := s.liveStats(ctx, service)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{
		ID:         uuid.NewString(),
		TenantID:   s.TenantID,
		Service:    service,
		Generation: gen,
		JobID:      jobID,
		CreatedAt:  time.Now().UTC(),
		ItemsLive:  liveN,
		BytesLive:  liveBytes,
	}
	_, err = s.DB.SQL.ExecContext(ctx, `
		INSERT INTO catalog_snapshots (id, tenant_id, service, generation, job_id, created_at, items_live, bytes_live)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.ID, snap.TenantID, snap.Service, snap.Generation, db.NullString(jobID),
		snap.CreatedAt, snap.ItemsLive, snap.BytesLive)
	if err != nil {
		return nil, err
	}
	for _, ch := range changes {
		if err := s.insertChange(ctx, gen, ch); err != nil {
			return nil, err
		}
	}
	if err := s.writeManifest(ctx, service, gen, snap.CreatedAt); err != nil {
		return nil, err
	}
	return snap, nil
}

func (s *Store) nextGeneration(ctx context.Context, service string) (int, error) {
	var gen sql.NullInt64
	err := s.DB.SQL.QueryRowContext(ctx, `
		SELECT MAX(generation) FROM catalog_snapshots WHERE tenant_id=? AND service=?`,
		s.TenantID, service).Scan(&gen)
	if err != nil {
		return 0, err
	}
	if !gen.Valid {
		return 1, nil
	}
	return int(gen.Int64) + 1, nil
}

func (s *Store) liveStats(ctx context.Context, service string) (int, int64, error) {
	var n int
	var bytes sql.NullInt64
	err := s.DB.SQL.QueryRowContext(ctx, `
		SELECT COUNT(1), COALESCE(SUM(size),0) FROM catalog_items
		WHERE tenant_id=? AND service=? AND deleted=0`, s.TenantID, service).Scan(&n, &bytes)
	if err != nil {
		return 0, 0, err
	}
	return n, bytes.Int64, nil
}

func (s *Store) insertChange(ctx context.Context, gen int, ch pendingChange) error {
	it := ch.Item
	_, err := s.DB.SQL.ExecContext(ctx, `
		INSERT INTO catalog_changes (id, tenant_id, service, generation, graph_item_id, op,
			mailbox, parent_path, name, blob_hash, size, mtime, content_type, subject, from_addr)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), s.TenantID, it.Service, gen, it.GraphItemID, ch.Op,
		it.Mailbox, it.ParentPath, it.Name, it.BlobHash, it.Size, db.NullTime(it.MTime),
		it.ContentType, it.Subject, it.FromAddr)
	return err
}

func (s *Store) upsertItem(ctx context.Context, it Item) error {
	if s.DB.Driver == db.DriverMySQL {
		_, err := s.DB.SQL.ExecContext(ctx, `
			INSERT INTO catalog_items (id, tenant_id, service, graph_item_id, mailbox, parent_path, name,
				blob_hash, size, mtime, deleted, content_type, subject, from_addr)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)
			ON DUPLICATE KEY UPDATE mailbox=VALUES(mailbox), parent_path=VALUES(parent_path), name=VALUES(name),
				blob_hash=VALUES(blob_hash), size=VALUES(size), mtime=VALUES(mtime), deleted=0,
				content_type=VALUES(content_type), subject=VALUES(subject), from_addr=VALUES(from_addr)`,
			uuid.NewString(), s.TenantID, it.Service, it.GraphItemID, it.Mailbox, it.ParentPath, it.Name,
			it.BlobHash, it.Size, db.NullTime(it.MTime), it.ContentType, it.Subject, it.FromAddr)
		return err
	}
	_, err := s.DB.SQL.ExecContext(ctx, `
		INSERT INTO catalog_items (id, tenant_id, service, graph_item_id, mailbox, parent_path, name,
			blob_hash, size, mtime, deleted, content_type, subject, from_addr)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)
		ON CONFLICT(tenant_id, service, graph_item_id) DO UPDATE SET
			mailbox=excluded.mailbox, parent_path=excluded.parent_path, name=excluded.name,
			blob_hash=excluded.blob_hash, size=excluded.size, mtime=excluded.mtime, deleted=0,
			content_type=excluded.content_type, subject=excluded.subject, from_addr=excluded.from_addr`,
		uuid.NewString(), s.TenantID, it.Service, it.GraphItemID, it.Mailbox, it.ParentPath, it.Name,
		it.BlobHash, it.Size, db.NullTime(it.MTime), it.ContentType, it.Subject, it.FromAddr)
	return err
}

func (s *Store) markDeleted(ctx context.Context, service, graphID string) error {
	_, err := s.DB.SQL.ExecContext(ctx, `
		UPDATE catalog_items SET deleted=1 WHERE tenant_id=? AND service=? AND graph_item_id=?`,
		s.TenantID, service, graphID)
	return err
}

func (s *Store) getItem(ctx context.Context, service, graphID string) (*Item, error) {
	row := s.DB.SQL.QueryRowContext(ctx, `
		SELECT graph_item_id, mailbox, parent_path, name, blob_hash, size, mtime, deleted, content_type, subject, from_addr
		FROM catalog_items WHERE tenant_id=? AND service=? AND graph_item_id=?`,
		s.TenantID, service, graphID)
	return scanItem(service, row)
}

func scanItem(service string, row interface{ Scan(dest ...any) error }) (*Item, error) {
	var it Item
	var mtime sql.NullTime
	var deleted int
	err := row.Scan(&it.GraphItemID, &it.Mailbox, &it.ParentPath, &it.Name, &it.BlobHash, &it.Size, &mtime, &deleted, &it.ContentType, &it.Subject, &it.FromAddr)
	if err != nil {
		return nil, err
	}
	it.Service = service
	it.Deleted = deleted != 0
	if mtime.Valid {
		it.MTime = mtime.Time
	}
	return &it, nil
}

func (s *Store) rematchID(ctx context.Context, it *Item) error {
	if _, err := s.getItem(ctx, it.Service, it.GraphItemID); err == nil {
		return nil
	} else if err != sql.ErrNoRows {
		return err
	}
	alts := importAliases(it.Service, it.GraphItemID, it.Mailbox, it.ParentPath, it.Name)
	for _, alt := range alts {
		old, err := s.getItem(ctx, it.Service, alt)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if it.Mailbox == "" {
			it.Mailbox = old.Mailbox
		}
		if it.ParentPath == "" {
			it.ParentPath = old.ParentPath
		}
		if it.Name == "" {
			it.Name = old.Name
		}
		_, err = s.DB.SQL.ExecContext(ctx, `
			UPDATE catalog_items SET graph_item_id=? WHERE tenant_id=? AND service=? AND graph_item_id=?`,
			it.GraphItemID, s.TenantID, it.Service, alt)
		if err != nil && db.IsUniqueViolation(err) {
			_, _ = s.DB.SQL.ExecContext(ctx, `
				DELETE FROM catalog_items WHERE tenant_id=? AND service=? AND graph_item_id=?`,
				s.TenantID, it.Service, alt)
			return nil
		}
		return err
	}
	return nil
}

func importAliases(service, graphID, mailbox, parent, name string) []string {
	if strings.HasPrefix(graphID, ImportEMLPrefix) || strings.HasPrefix(graphID, ImportPathPrefix) {
		return nil
	}
	var alts []string
	if service == "exchange" {
		alts = append(alts, ImportEMLPrefix+shortID(graphID))
	}
	rel := strings.Trim(parent+"/"+name, "/")
	if mailbox != "" && rel != "" {
		alts = append(alts, ImportPathPrefix+mailbox+"/"+rel)
	} else if mailbox != "" && name != "" {
		alts = append(alts, ImportPathPrefix+mailbox+"/"+name)
	}
	return alts
}

func shortID(mid string) string {
	sum := sha256.Sum256([]byte(mid))
	return hex.EncodeToString(sum[:])[:10]
}

func (s *Store) HasLiveItems(ctx context.Context, service string) (bool, error) {
	n, err := countLiveItems(ctx, s.DB, s.TenantID, service)
	return n > 0, err
}

func (s *Store) HasSnapshots(ctx context.Context) (bool, error) {
	return s.HasSnapshotsFor(ctx, "")
}

func (s *Store) HasSnapshotsFor(ctx context.Context, service string) (bool, error) {
	n, err := countSnapshots(ctx, s.DB, s.TenantID, service)
	return n > 0, err
}

func (s *Store) ListSnapshots(ctx context.Context, service string) ([]Snapshot, error) {
	q := `SELECT id, tenant_id, service, generation, COALESCE(job_id,''), created_at, items_live, bytes_live
		FROM catalog_snapshots WHERE tenant_id=?`
	args := []any{s.TenantID}
	if service != "" {
		q += ` AND service=?`
		args = append(args, service)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.DB.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		var sn Snapshot
		if err := rows.Scan(&sn.ID, &sn.TenantID, &sn.Service, &sn.Generation, &sn.JobID, &sn.CreatedAt, &sn.ItemsLive, &sn.BytesLive); err != nil {
			return nil, err
		}
		out = append(out, sn)
	}
	return out, rows.Err()
}

func (s *Store) GetSnapshot(ctx context.Context, id string) (*Snapshot, error) {
	row := s.DB.SQL.QueryRowContext(ctx, `
		SELECT id, tenant_id, service, generation, COALESCE(job_id,''), created_at, items_live, bytes_live
		FROM catalog_snapshots WHERE tenant_id=? AND id=?`, s.TenantID, id)
	var sn Snapshot
	if err := row.Scan(&sn.ID, &sn.TenantID, &sn.Service, &sn.Generation, &sn.JobID, &sn.CreatedAt, &sn.ItemsLive, &sn.BytesLive); err != nil {
		return nil, err
	}
	return &sn, nil
}

func (s *Store) ListMailboxes(ctx context.Context, service string) ([]string, error) {
	rows, err := s.DB.SQL.QueryContext(ctx, `
		SELECT DISTINCT mailbox FROM catalog_items
		WHERE tenant_id=? AND service=? AND deleted=0 AND mailbox!=''
		ORDER BY mailbox`, s.TenantID, service)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListFolders(ctx context.Context, service, mailbox string) ([]string, error) {
	rows, err := s.DB.SQL.QueryContext(ctx, `
		SELECT DISTINCT parent_path FROM catalog_items
		WHERE tenant_id=? AND service=? AND deleted=0 AND mailbox=? AND parent_path!=''
		ORDER BY parent_path`, s.TenantID, service, mailbox)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		top := strings.SplitN(strings.Trim(p, "/"), "/", 2)[0]
		if top == "" || seen[top] {
			continue
		}
		seen[top] = true
		out = append(out, top)
	}
	return out, rows.Err()
}

func (s *Store) ListLiveItems(ctx context.Context, service, mailbox, folder string) ([]Item, error) {
	q := `SELECT graph_item_id, mailbox, parent_path, name, blob_hash, size, mtime, deleted, content_type, subject, from_addr
		FROM catalog_items WHERE tenant_id=? AND service=? AND deleted=0`
	args := []any{s.TenantID, service}
	if mailbox != "" {
		q += ` AND mailbox=?`
		args = append(args, mailbox)
		if folder != "" {
			q += ` AND parent_path=?`
			args = append(args, folder)
		}
	}
	q += ` ORDER BY mailbox, parent_path, name`
	rows, err := s.DB.SQL.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(service, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

func (s *Store) GetBlob(hash string) ([]byte, error) {
	return s.Blobs.Get(hash)
}

func (s *Store) writeManifest(ctx context.Context, service string, gen int, created time.Time) error {
	items, err := s.ListLiveItems(ctx, service, "", "")
	if err != nil {
		return err
	}
	man := Manifest{Service: service, Generation: gen, CreatedAt: created}
	for _, it := range items {
		man.Items = append(man.Items, ManifestEntry{Path: it.RelPath(), Hash: it.BlobHash, Size: it.Size})
	}
	raw, err := json.Marshal(man)
	if err != nil {
		return err
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return err
	}
	comp := enc.EncodeAll(raw, nil)
	enc.Close()
	path, err := s.manifestPath(service, gen)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sealed, err := encryptBytes(s.Password, comp)
	if err != nil {
		return err
	}
	return os.WriteFile(path, sealed, 0o600)
}

func (s *Store) ReadManifest(service string, gen int) (*Manifest, error) {
	path, err := s.manifestPath(service, gen)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	comp, err := decryptBytes(s.Password, raw)
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	plain, err := dec.DecodeAll(comp, nil)
	if err != nil {
		return nil, err
	}
	var man Manifest
	if err := json.Unmarshal(plain, &man); err != nil {
		return nil, err
	}
	return &man, nil
}

func (s *Store) manifestPath(service string, gen int) (string, error) {
	if _, err := storage.GuardPath(service); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%d.json.zst", gen)
	return storage.EnsureSubpath(s.Root, filepath.Join("manifests", service, name))
}

func (s *Store) deleteManifest(service string, gen int) {
	path, err := s.manifestPath(service, gen)
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

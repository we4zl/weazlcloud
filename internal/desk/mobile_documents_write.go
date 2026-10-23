package desk

import (
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"path"
	"time"

	"github.com/bprendie/weazlcloud/internal/catalog"
	"github.com/bprendie/weazlcloud/internal/filesvc"
	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

func (h *Handler) mobileDocumentsPut(w http.ResponseWriter, r *http.Request, res *filesvc.Resource, user users.User, name string) {
	if r.ContentLength < 0 {
		nativeJSONcode(w, 411, "length_required", "document length is required")
		return
	}
	if r.ContentLength > documentsMaxBytes {
		nativeJSONcode(w, 413, "too_large", "document exceeds the 256 MiB limit")
		return
	}
	digest := r.Header.Get("X-Weazl-Content-SHA256")
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 {
		nativeJSONcode(w, 400, "checksum_required", "a SHA-256 content checksum is required")
		return
	}
	digest = hex.EncodeToString(decoded)
	grant, err := h.users.GrantForRequest(r, users.DocumentsWrite)
	if err != nil {
		mobileDocumentsError(w, err)
		return
	}
	var body io.Reader = http.MaxBytesReader(w, r.Body, documentsMaxBytes)
	if h.quota != nil {
		var release func()
		if res.Lib.SharedWritesEnabled() {
			body, release, err = h.quota.GuardSharedWrite(user.ID, h.users.Count(), 0, 0, r.ContentLength, body)
		} else {
			body, release, err = h.quota.GuardReader(user.ID, h.users.Count(), 0, 0, r.ContentLength, body)
		}
		if err != nil {
			mobileDocumentsError(w, err)
			return
		}
		defer release()
	}
	ctx, cancel := mobileVaultContext(r.Context(), res)
	defer cancel()
	var file catalog.File
	err = res.Lib.WithBackup(ctx, func(tx *library.BackupTransaction) error {
		if err := h.users.CheckDeviceGrant(grant, users.DocumentsWrite); err != nil {
			return err
		}
		tx.SetCommitGuard(func(publish func() error) error { return h.users.WithDeviceGrant(grant, publish, users.DocumentsWrite) })
		plan, existing, err := mobileDocumentPlan(tx, name, false)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.Hash != digest || existing.Size != r.ContentLength {
				return catalog.ErrConflict
			}
			file = *existing
			return tx.FinishBackup(catalog.BackupMutation{Files: []catalog.File{file}})
		}
		file = plan.Files[0]
		file.Size, file.Hash = r.ContentLength, digest
		operation, err := catalog.NewBackupIdentity()
		if err != nil {
			return err
		}
		file, err = tx.StoreReader(operation, file, body)
		if err != nil {
			return err
		}
		plan.Files[0] = file
		if err := tx.Publish(plan); err != nil {
			return errors.Join(err, tx.AbortBackup(plan))
		}
		return tx.FinishBackup(plan)
	})
	if err != nil {
		mobileDocumentsError(w, err)
		return
	}
	writeJSON(w, 200, fileView{Path: file.Path, Size: file.Size, Mtime: file.Mtime})
}

func (h *Handler) mobileDocumentsFolder(r *http.Request, res *filesvc.Resource, name string) error {
	grant, err := h.users.GrantForRequest(r, users.DocumentsWrite)
	if err != nil {
		return err
	}
	ctx, cancel := mobileVaultContext(r.Context(), res)
	defer cancel()
	return res.Lib.WithBackup(ctx, func(tx *library.BackupTransaction) error {
		tx.SetCommitGuard(func(publish func() error) error { return h.users.WithDeviceGrant(grant, publish, users.DocumentsWrite) })
		plan, _, err := mobileDocumentPlan(tx, name, true)
		if err != nil {
			return err
		}
		if len(plan.Files) == 0 {
			return h.users.CheckDeviceGrant(grant, users.DocumentsWrite)
		}
		return tx.Publish(plan)
	})
}

// The library mutation lock covers planning, encrypted storage and publication.
// Existing files are immutable; missing parents and the new file publish together.
func mobileDocumentPlan(tx *library.BackupTransaction, name string, folder bool) (catalog.BackupMutation, *catalog.File, error) {
	var plan catalog.BackupMutation
	var existing *catalog.File
	for current := name; current != "."; current = path.Dir(current) {
		wantFolder := current != name || folder
		if old, ok := tx.Catalog().Get(current); ok {
			if old.Folder != wantFolder {
				return plan, nil, catalog.ErrConflict
			}
			plan.Guards = append(plan.Guards, old)
			if current == name {
				existing = &old
			}
			continue
		}
		id, err := catalog.NewBackupIdentity()
		if err != nil {
			return plan, nil, err
		}
		now := time.Now().UTC()
		plan.Absent = append(plan.Absent, current)
		plan.Files = append(plan.Files, catalog.File{EntryID: id, Revision: 1, Path: current, Folder: wantFolder, Present: true, Mtime: now, ImportedAt: now})
	}
	return plan, existing, nil
}

func mobileDocumentsError(w http.ResponseWriter, err error) {
	status, code := 500, "internal_error"
	switch {
	case errors.Is(err, users.ErrNoSession):
		status, code = 401, "authentication_required"
	case errors.Is(err, users.ErrInsufficientScope):
		status, code = 403, "insufficient_scope"
	case errors.Is(err, vault.ErrLocked):
		status, code = 401, "vault_locked"
	case errors.Is(err, catalog.ErrNotFound), errors.Is(err, library.ErrFileNotFound):
		status, code = 404, "not_found"
	case errors.Is(err, catalog.ErrConflict), errors.Is(err, catalog.ErrRevisionMismatch), errors.Is(err, library.ErrFolderCursor):
		status, code = 409, "revision_conflict"
	case errors.Is(err, quota.ErrExceeded):
		status, code = 507, "quota_exceeded"
	case errors.Is(err, library.ErrBackupChecksum):
		status, code = 422, "checksum_mismatch"
	}
	nativeJSONcode(w, status, code, code)
}

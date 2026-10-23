package desk

import (
	"net/http"
	"strings"

	"github.com/bprendie/weazlcloud/internal/library"
	"github.com/bprendie/weazlcloud/internal/users"
	"github.com/bprendie/weazlcloud/internal/vault"
)

const documentsRoot = "Weazldocs"
const documentsMaxBytes = 256 << 20

func (h *Handler) tryMobileDocuments(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/v1/documents" && !strings.HasPrefix(r.URL.Path, "/api/v1/documents/") {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if h.users == nil || h.registry == nil {
		nativeJSONcode(w, 401, "authentication_required", "owner account required")
		return true
	}
	device, err := h.users.DeviceForRequest(r)
	if err != nil {
		mobileFilesError(w, err)
		return true
	}
	if r.Method != http.MethodGet && !mutating(r) {
		nativeJSONcode(w, 403, "forbidden", "request validation required")
		return true
	}
	user, exists := h.users.User(device.OwnerID)
	if !exists || user.Disabled || user.Deleting {
		nativeJSONcode(w, 401, "authentication_required", "owner account required")
		return true
	}
	ctx, release, ok := h.registry.Enter(r.Context(), user.ID)
	if !ok {
		mobileFilesError(w, users.ErrNoSession)
		return true
	}
	defer release()
	r = r.WithContext(ctx)
	user, exists = h.users.User(device.OwnerID)
	if !exists || user.Disabled || user.Deleting {
		mobileDocumentsError(w, users.ErrNoSession)
		return true
	}
	res := h.registry.For(user)
	if res.Vault == nil || !res.Vault.Unlocked() || res.Lib == nil {
		mobileFilesError(w, vault.ErrLocked)
		return true
	}
	path := r.URL.Query().Get("path")
	switch {
	case r.URL.Path == "/api/v1/documents" && r.Method == http.MethodGet:
		h.mobileDocumentsList(w, r, res.Lib, path)
	case r.URL.Path == "/api/v1/documents/folder" && r.Method == http.MethodPost:
		if !withinDocuments(path) {
			nativeJSONcode(w, 400, "invalid_path", "folder must be inside Weazldocs")
			return true
		}
		if err := h.mobileDocumentsFolder(r, res, path); err != nil {
			mobileDocumentsError(w, err)
			return true
		}
		writeJSON(w, 201, map[string]string{"path": path})
	case r.URL.Path == "/api/v1/documents/content" && r.Method == http.MethodGet:
		if !withinDocuments(path) || path == documentsRoot {
			nativeJSONcode(w, 400, "invalid_path", "file must be inside Weazldocs")
			return true
		}
		file, err := res.Lib.Metadata(ctx, path)
		if err != nil {
			mobileDocumentsError(w, err)
			return true
		}
		if file.Folder {
			nativeJSONcode(w, 400, "invalid_path", "path identifies a folder")
			return true
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment")
		if err := res.Lib.StreamTo(ctx, path, w); err != nil {
			return true
		}
	case r.URL.Path == "/api/v1/documents/content" && r.Method == http.MethodPut:
		if !withinDocuments(path) || path == documentsRoot {
			nativeJSONcode(w, 400, "invalid_path", "file must be inside Weazldocs")
			return true
		}
		h.mobileDocumentsPut(w, r, res, user, path)
	default:
		nativeJSONcode(w, 404, "not_found", "document route not found")
	}
	return true
}

func (h *Handler) mobileDocumentsList(w http.ResponseWriter, r *http.Request, lib *library.Library, folder string) {
	if folder == "" {
		folder = documentsRoot
	}
	if !withinDocuments(folder) {
		nativeJSONcode(w, 400, "invalid_path", "folder must be inside Weazldocs")
		return
	}
	page, err := lib.ListFolderPage(r.Context(), folder, "name", false, library.FolderPageMaximum, r.URL.Query().Get("cursor"))
	if err != nil {
		mobileDocumentsError(w, err)
		return
	}
	files := make([]fileView, 0, len(page.Files))
	for _, file := range page.Files {
		if withinDocuments(file.Path) {
			files = append(files, fileView{Path: file.Path, Folder: file.Folder, Size: file.Size, Mtime: file.Mtime})
		}
	}
	writeJSON(w, 200, map[string]any{"files": files, "next_cursor": page.NextCursor, "generation": page.Generation})
}

func withinDocuments(name string) bool {
	clean, err := library.CleanPath(name)
	return err == nil && clean == name && (name == documentsRoot || strings.HasPrefix(name, documentsRoot+"/")) && !strings.Contains(name, "\\")
}

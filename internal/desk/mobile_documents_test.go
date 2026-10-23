package desk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bprendie/weazlcloud/internal/capsule"
	"github.com/bprendie/weazlcloud/internal/quota"
	"github.com/bprendie/weazlcloud/internal/sharedstore"
	"github.com/bprendie/weazlcloud/internal/users"
)

func TestScopedDocumentsStayInsideDedicatedVaultFolder(t *testing.T) {
	for _, backend := range []string{"restic", "shared"} {
		t.Run(backend, func(t *testing.T) { exerciseDocumentRoutes(t, backend) })
	}
}

func exerciseDocumentRoutes(t *testing.T, backend string) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic unavailable")
	}
	dir := t.TempDir()
	store, err := users.New(filepath.Join(dir, "users.json"), filepath.Join(dir, "users"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := store.Create("writer", "writer-password", true)
	if err != nil {
		t.Fatal(err)
	}
	h := NewMulti(store, capsule.New(filepath.Join(dir, "capsules")), quota.New(dir), "", "", dir)
	if backend == "shared" {
		shared, err := sharedstore.Open(dir, sharedstore.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer shared.Close()
		h.registry.ConfigureShared(shared, true)
	}
	res := h.registry.For(owner)
	if err := res.Vault.Forge([]byte("writer-vault"), []byte("writer-vault")); err != nil {
		t.Fatal(err)
	}
	defer res.LockVault()
	device, token, err := store.CreateScopedDevice(owner.ID, "Weazldocs", []string{users.DocumentsRead, users.DocumentsWrite})
	if err != nil {
		t.Fatal(err)
	}
	_, readToken, err := store.CreateScopedDevice(owner.ID, "reader", []string{users.DocumentsRead})
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := store.CreateScopedDevice(owner.ID, "other", []string{users.PhotosRead})
	if err != nil {
		t.Fatal(err)
	}

	request := func(method, target, credential string, body []byte, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, target, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+credential)
		r.Header.Set("X-Weazl-Desk", "1")
		if method == http.MethodPut {
			r.Header.Set("X-Weazl-Content-SHA256", fmt.Sprintf("%x", sha256.Sum256(body)))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, target, w.Code, want, w.Body.String())
		}
		if len(target) >= len("/api/v1/documents") && target[:len("/api/v1/documents")] == "/api/v1/documents" && w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("missing private response cache policy")
		}
		return w
	}
	path := "Weazldocs/test/revisions/0001.wzdoc"
	request(http.MethodGet, "/api/v1/documents", token, nil, 404)
	request(http.MethodGet, "/api/v1/documents", "", nil, 401)
	request(http.MethodPost, "/api/v1/documents/folder?path=Weazldocs/test", token, nil, 201)
	request(http.MethodGet, "/api/v1/documents/content?path="+path, token, nil, 404)
	request(http.MethodPut, "/api/v1/documents/content?path="+path, token, []byte("revision one"), 200)
	request(http.MethodPut, "/api/v1/documents/content?path="+path, token, []byte("revision one"), 200)
	request(http.MethodPut, "/api/v1/documents/content?path="+path, token, []byte("overwrite"), 409)
	got := request(http.MethodGet, "/api/v1/documents/content?path="+path, token, nil, 200)
	if got.Body.String() != "revision one" {
		t.Fatalf("roundtrip content %q", got.Body.String())
	}
	list := request(http.MethodGet, "/api/v1/documents?path=Weazldocs/test/revisions", token, nil, 200)
	var body struct {
		Files []fileView `json:"files"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &body); err != nil || len(body.Files) != 1 || body.Files[0].Path != path {
		t.Fatalf("list=%s err=%v", list.Body.String(), err)
	}
	request(http.MethodGet, "/api/v1/documents/content?path=Photos/secret.jpg", token, nil, 400)
	request(http.MethodPut, "/api/v1/documents/content?path=Weazldocs/../Photos/secret.jpg", token, []byte("no"), 400)
	request(http.MethodPut, "/api/v1/documents/content?path=Weazldocs/test/child", other, []byte("no"), 403)
	request(http.MethodPut, "/api/v1/documents/content?path=Weazldocs/test/child", readToken, []byte("no"), 403)
	request(http.MethodGet, "/api/v1/documents/content?path="+path, readToken, nil, 200)
	request(http.MethodGet, "/api/v1/files/sync?limit=10", token, nil, 403)
	request(http.MethodPost, "/api/uploads", token, []byte(`{}`), 403)
	request(http.MethodGet, "/api/v1/documents?contract_version=2", token, nil, 400)
	quotaState, err := h.quota.Status(1)
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.quota.Reserve(owner.ID, 1, 0, 0, int64(quotaState.Limit-quotaState.Used-quotaState.Reserved))
	if err != nil {
		t.Fatal(err)
	}
	request(http.MethodPut, "/api/v1/documents/content?path=Weazldocs/test/quota", token, []byte("no"), 507)
	release()
	checkPut := func(body io.Reader, length int64, digest string, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/documents/content?path=Weazldocs/test/rejected", body)
		r.ContentLength = length
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Weazl-Desk", "1")
		r.Header.Set("X-Weazl-Content-SHA256", digest)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("put status=%d want=%d body=%s", w.Code, want, w.Body.String())
		}
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("accepted")))
	checkPut(bytes.NewReader(nil), documentsMaxBytes+1, digest, 413)
	checkPut(bytes.NewReader(nil), -1, digest, 411)
	checkPut(bytes.NewReader(nil), 0, "", 400)
	checkPut(bytes.NewBufferString("tampered"), 8, digest, 422)
	request(http.MethodGet, "/api/v1/documents/content?path=Weazldocs/test/rejected", token, nil, 404)
	res.LockVault()
	request(http.MethodGet, "/api/v1/documents", token, nil, 401)
	if err := res.Vault.Unlock([]byte("writer-vault")); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, "/api/v1/documents/content?path="+path, token, nil, 200)
	store.SetClock(func() time.Time { return time.Now().Add(100 * 24 * time.Hour) })
	request(http.MethodGet, "/api/v1/documents", token, nil, 401)
	store.SetClock(nil)
	reader := &documentReadHook{Reader: bytes.NewBufferString("accepted"), hook: func() {
		if err := store.RevokeDevice(owner.ID, device.ID); err != nil {
			t.Error(err)
		}
	}}
	checkPut(reader, 8, digest, 401)
	request(http.MethodGet, "/api/v1/documents/content?path=Weazldocs/test/rejected", readToken, nil, 404)
	if err := res.Lib.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	request(http.MethodGet, "/api/v1/documents/content?path=Weazldocs/test/rejected", readToken, nil, 404)
}

type documentReadHook struct {
	io.Reader
	hook func()
}

func (r *documentReadHook) Read(p []byte) (int, error) {
	if r.hook != nil {
		hook := r.hook
		r.hook = nil
		hook()
	}
	return r.Reader.Read(p)
}

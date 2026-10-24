# Dedicated document device access

This additive contract v1 API gives Weazldocs devices access only beneath the
case-sensitive `Weazldocs` folder. Existing login, vault unlock, Photos, Files,
backup, WebDAV and generic browser routes retain their existing authorization.
No encrypted data format or database migration is introduced.

An authenticated account with an unlocked vault can enroll a device using
`POST /api/v1/devices`, requesting `documents:read` and `documents:write`.
Write requires read. Existing devices gain no new grants automatically.

| Request | Scope | Behavior |
| --- | --- | --- |
| `GET /api/v1/documents?path=Weazldocs&cursor=...` | read | Bounded folder page; missing folder returns 404. |
| `POST /api/v1/documents/folder?path=Weazldocs/...` | write | Creates missing parent folders atomically; existing folders are idempotent. |
| `GET /api/v1/documents/content?path=Weazldocs/...` | read | Streams existing file; missing file returns 404. |
| `PUT /api/v1/documents/content?path=Weazldocs/...` | write | Creates an immutable revision and missing parents; matching hash/size retries succeed, differing content returns 409. |

Requests use a Bearer device credential and the existing v1 negotiation header.
Mutation requests retain the existing `X-Weazl-Desk: 1` request validation.
PUT requires Content-Length (at most 256 MiB) and `X-Weazl-Content-SHA256`
containing the expected 32-byte SHA-256 digest in hex. Storage verifies both
length and checksum before publication. Quota admission uses the existing
backend-specific reservations. Missing length returns 411, bad checksum 422,
excess size 413 and quota exhaustion 507.

The new writer uses the existing library transaction and encrypted backend
streaming primitives, without a recoverable plaintext generic-upload stage.
The catalog mutation and credential invalidation are serialized at publication;
revocation during transfer prevents the write from becoming visible. Vault
session cancellation and owner lifecycle leases are retained. Catalog publication
syncs its parent directory; the client still verifies content by reading it back.

Legacy scopes cannot call the new routes. Docs credentials cannot call generic
upload, Photos or Files routes. Root escape, unclean paths, folder/file type
conflicts and attempts to replace immutable files are rejected. Standard browser
access remains capable of managing its owner's files; Docs must treat external
changes as conflicts.

Validation includes both Restic and shared storage, checksum/size rejection,
quota denial, in-flight revocation, locked/expired credentials, retry without
overwrite, persisted reads, and previous-image rollback. Run the dedicated
container smoke with `WEAZLCLOUD_IMAGE` and `WEAZLCLOUD_PREVIOUS_IMAGE` set to
local images: `python3 scripts/smoke-documents.py`. It creates only disposable
accounts, loopback containers, and volumes; it accepts no live-server URL.

## Requests and responses

See the [complete API reference](api-reference.md#weazldocs) for the
shared authentication and error conventions. Use these headers on each request:

```http
Authorization: Bearer DEVICE_TOKEN
X-Weazl-Mobile-Contract: 1
```

Add `X-Weazl-Desk: 1` to POST and PUT. A supplied Origin must match the request
host. Cookie authentication alone does not authorize these document routes.
The owner's vault must be unlocked. Responses use `Cache-Control: private, no-store`.

Listing defaults to `path=Weazldocs`, sorts by name ascending, and returns at most
200 entries per page. Pass the opaque `next_cursor` unchanged with the same path;
an empty cursor ends enumeration. The response is:

```json
{
  "files": [
    {"path":"Weazldocs/Notes","folder":true,"size":0,"mtime":"2026-10-24T12:00:00Z"},
    {"path":"Weazldocs/readme.txt","size":5,"mtime":"2026-10-24T12:00:00Z"}
  ],
  "next_cursor":"",
  "generation":3
}
```

`folder` is omitted for regular files. These entries do not include native Files
IDs or revision numbers. A changed listing generation can invalidate a cursor;
restart enumeration on 409 `revision_conflict`.

Folder POST takes the path in the query and no JSON body. It returns 201
`{"path":"Weazldocs/Notes"}`, including when the folder already exists. Missing
parents publish with the folder in one catalog transaction.

Content GET returns an attachment with `application/octet-stream`. This handler
has no HEAD, Range, conditional-read, delete, move, or overwrite contract. Content
PUT returns 200 with a file entry shaped like the listing entries. Existing
matching hash/size is an idempotent success; changed content must use a new path.
A successful immutable retry can return without consuming the repeated body.

Paths must exactly equal their cleaned form, begin with `Weazldocs/` for content,
and contain no backslash or root escape. Folder operations can target `Weazldocs`
itself. A folder in a file position, or a file in a required parent position,
conflicts rather than being replaced.

| Status | Meaning |
| --- | --- |
| 400 | Invalid path or malformed length/checksum metadata |
| 401 | Authentication required or vault locked; unlocking does not require device reenrollment |
| 403 | Insufficient scope or missing mutation validation |
| 404 | Missing file/folder |
| 409 | Immutable content/type conflict or invalidated folder cursor |
| 411 | Missing Content-Length |
| 413 | Body exceeds 256 MiB |
| 422 | Received content does not match its declared checksum/size |
| 507 | Quota exhausted |

Errors use the native JSON envelope described in the main reference. Do not
retry conflicts as overwrites or classify a locked vault as revoked credentials.

## Example immutable upload

Set `cloud_origin` to your Desk HTTPS origin and `device_token` to an enrolled
Documents credential. The example requires curl and sha256sum:

```sh
cloud_origin='https://cloud.example.test'
device_token='REPLACE_WITH_DEVICE_TOKEN'
document_file='./note.txt'
document_sha=$(sha256sum "$document_file" | cut -d ' ' -f 1)
curl --fail-with-body -X PUT \
  -H "Authorization: Bearer $device_token" \
  -H 'X-Weazl-Mobile-Contract: 1' -H 'X-Weazl-Desk: 1' \
  -H "X-Weazl-Content-SHA256: $document_sha" \
  --data-binary "@$document_file" \
  "$cloud_origin/api/v1/documents/content?path=Weazldocs%2FNotes%2Fnote.txt"
curl --fail-with-body \
  -H "Authorization: Bearer $device_token" -H 'X-Weazl-Mobile-Contract: 1' \
  "$cloud_origin/api/v1/documents/content?path=Weazldocs%2FNotes%2Fnote.txt" \
  --output ./note.readback.txt
sha256sum "$document_file" ./note.readback.txt
```

The first PUT creates missing folders. Compare the read-back digest before
recording the document as verified. Choose a new immutable filename for the next
revision; browser-side edits can otherwise produce a conflict.

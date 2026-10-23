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

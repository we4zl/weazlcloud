# WeazlCloud API reference

Implementation reference, reviewed October 24, 2026. This guide covers the
multiuser server's Desk HTTP API, native device APIs, public Share API, and
WebDAV listener. Examples use placeholder hosts and credentials. Paths and
methods below describe implemented operations, not proposed endpoints.

## Contents

1. [Listeners and conventions](#listeners-and-conventions)
2. [Authentication and authorization](#authentication-and-authorization)
3. [Errors and retry rules](#errors-and-retry-rules)
4. [Health and discovery](#health-and-discovery)
5. [Accounts, sessions and vaults](#accounts-sessions-and-vaults)
6. [Administration, settings and capacity](#administration-settings-and-capacity)
7. [Library files, previews and events](#library-files-previews-and-events)
8. [Browser resumable uploads](#browser-resumable-uploads)
9. [Trash and owner ZIP downloads](#trash-and-owner-zip-downloads)
10. [Takeout imports](#takeout-imports)
11. [Device enrollment and credentials](#device-enrollment-and-credentials)
12. [Photos browsing, editing and organization](#photos-browsing-editing-and-organization)
13. [Photo and backup upload transports](#photo-and-backup-upload-transports)
14. [Photo synchronization and source recovery](#photo-synchronization-and-source-recovery)
15. [Native Files and recurring backups](#native-files-and-recurring-backups)
16. [Weazldocs](#weazldocs)
17. [Owner-created grab links](#owner-created-grab-links)
18. [Public Share API](#public-share-api)
19. [WebDAV](#webdav)
20. [Limits, compatibility and source map](#limits-compatibility-and-source-map)

The domain contracts are part of this reference: [Photos](photo-api.md),
[native mobile](mobile-api.md), [documents](documents-api.md),
[content lookup](photo-content-lookup.md),
[batched upload status](mobile-upload-status-batch.md), and
[source recovery](mobile-source-recovery.md). They provide extended DTOs,
state-machine rules and examples alongside the endpoint inventory here.
[photo-api.yaml](photo-api.yaml) and [mobile-api.yaml](mobile-api.yaml) are
machine-readable **domain** specifications; neither alone describes every
Desk, Share or WebDAV operation.

## Listeners and conventions

| Listener | Native default | Purpose |
| --- | --- | --- |
| Desk | `http://127.0.0.1:7272` | All `/api/*`, discovery, owner UI |
| Share | `http://127.0.0.1:7273` | `/g/*` recipient access, no owner login/unlock |
| Drive | `http://127.0.0.1:7274` | WebDAV with account Basic authentication |

Every listener serves `GET /live` and `GET /ready`. Deployments may publish
separate HTTPS hostnames instead of these ports. Use the administrator-provided
Desk origin and returned grab URLs; do not derive a Share hostname from a Desk
connection alias. Default Compose services expose container ports without
publishing host ports. See [deployment](docker-compose.md).

JSON requests use `Content-Type: application/json`. Send `X-Weazl-Desk: 1` on
Desk POST, PUT, PATCH and DELETE requests, including login/bootstrap and read-like
POST queries. When `Origin` is present, its parsed host (including port) must
match the request host. A valid bearer token does not bypass this guard.
Share guest requests and WebDAV use their separate protocols below.

Path parameters in braces are placeholders; URL-encode query values and path
segments. Library paths are owner-relative, such as `Music/album/track.flac`,
not host paths or `/data/...`. Folder-list root is an empty `path`. Documents
and backups apply stricter canonical-path validation. IDs are opaque: a photo
asset ID, device ID, upload ID, backup receipt ID and grab ID are not interchangeable.
Byte sizes and offsets are integers, timestamps are RFC 3339 strings unless a
field explicitly uses calendar dates, and optional fields may be omitted.

There is no common `{data:...}` success envelope. Some routes return arrays,
some objects, and content routes return bytes. Preserve unknown response fields
for forward compatibility. Do not assume identical pagination, error or conditional
request behavior across browser, Photos and native Files routes.

## Authentication and authorization

| Credential | How obtained | Where accepted |
| --- | --- | --- |
| `weazl_session` cookie | Bootstrap or account login | Owner Desk routes; admin operations also require administrator role |
| `Authorization: Bearer <token>` | Owner enrolls a scoped device | Exact permitted methods/routes for that device's scopes |
| HTTP Basic username/password | Existing local account | Drive listener only |
| Grab ID plus optional passphrase | Owner shares returned URL | Share listener; gallery operations exchange this for a capsule session |

Cookie sessions are in-memory, expire after 24 hours, and do not survive server
restart. Cookies have `HttpOnly`, `SameSite=Lax`, `Path=/`; `Secure` follows
`WEAZLCLOUD_SECURE_COOKIES`. Login and vault unlock are separate operations.
Logout invalidates the session; it is not the vault-lock endpoint. An administrator
cannot read another owner's vault through the API.

Private reads and uploads generally require an unlocked owner vault. Devices
contain no vault key and cannot call account login/unlock. A restart requires owner
login/unlock before private native work resumes. WebDAV is different: after
successful Basic authentication, it invokes the node-backed vault unlock path.
A local application's screen lock is not a server vault lock.

Bearer credentials are 64 hexadecimal characters encoding 32 random bytes.
Supplying an Authorization header does not permit fallback to an owner cookie
when bearer authentication fails. Enrollment with omitted `scopes` gives the
legacy `photos:v1` compatibility identity; explicit `[]` gives no resource grants.
Explicit grants are:

| Scope | Operations |
| --- | --- |
| `photos:read` | Photo metadata/content, lookup, selections and sync |
| `photos:write` | Photo ingestion and mutations; upload status also requires this scope |
| `files:read` | Library/native Files reads, file sync, owner ZIP preparation |
| `files:write` | Generic Library mutations |
| `backup:write` | Recurring backup sources/uploads; requires `files:write` |
| `documents:read` | Dedicated Weazldocs folder/content reads |
| `documents:write` | Immutable Weazldocs writes; requires `documents:read` |
| `grabs:read` | Owner capsule listing and idempotent-operation lookup |
| `grabs:write` | Mint/revoke; file mint also needs `files:read`, photo mint `photos:read` |
| `storage:read` | Quota projection |

Write does not generally imply read. Device management self-status, rotation,
self-revoke and mobile discovery have intrinsic grants. Legacy `photos:v1` has
only its explicitly maintained compatibility allowlist; it is not an enrollable
explicit scope. See the [exact method/scope matrix](mobile-api.md#exact-bearer-route-allowlist-at-documentation-inspection).

**Table notation:** `session` means owner-cookie authentication; `admin` means
administrator cookie; `F:R/F:W`, `P:R/P:W`, `D:R/D:W`, `G:R/G:W`, `B:W`, `S:R`
mean the corresponding Files, Photos, Documents, Grabs, Backup and Storage scopes.
`session / F:R`, for example, allows either the owner cookie or a scoped bearer.
`unlocked` is an additional requirement, not a replacement credential.

### Native contract negotiation

For `/api/v1/*` and `/.well-known/weazlcloud`, send
`X-Weazl-Mobile-Contract: 1` or query `contract_version=1`. Both may be present
if both equal `1`. Omission retains legacy behavior. Empty, duplicate or future
values and conflicting header/query values return 400 with
`code: unsupported_contract_version` and `supported_contract_versions: [1]`.
The contract version is independent of encrypted catalog/schema versions.

### Minimal owner session example

Create private JSON request files containing the indicated bodies; do not commit
real credentials. `login.json` contains `{"username":"alice","password":"..."}`;
`unlock.json` contains `{"passphrase":"..."}`. These calls target an existing account:

```sh
umask 077
cloud_origin='https://desk.example.test'
curl --fail-with-body -c cookies.txt -H 'X-Weazl-Desk: 1' \
  -H 'Content-Type: application/json' --data-binary @login.json \
  "$cloud_origin/api/login"
curl --fail-with-body -b cookies.txt -H 'X-Weazl-Desk: 1' \
  -H 'Content-Type: application/json' --data-binary @unlock.json \
  "$cloud_origin/api/unlock"
curl --fail-with-body -b cookies.txt --get --data-urlencode 'path=Music' \
  "$cloud_origin/api/library/page"
```

## Errors and retry rules

Most JSON routes return `{"error":"message"}` on failure. Newer native routes
also use `code`, sometimes `retryable` or `resync_required`. Older handlers may
send JSON-looking text via `http.Error`, or a plain-text 404; inspect status and
Content-Type before decoding. A failed stream after response headers may truncate
the body instead of returning a new JSON error. Verify expected size/hash before
marking a download complete.

| HTTP status | Meaning and client action |
| --- | --- |
| 200 / 201 / 202 | Success / created / work accepted. A queued job is not proof the original is stored. |
| 206 / 304 | Partial content / unchanged representation on routes supporting these semantics |
| 400 | Invalid parameters/body; also browser Library stale cursors and several legacy storage/path errors |
| 401 | Missing/invalid authentication, or a vault-lock failure on older routes; inspect the error |
| 403 | Missing scope, wrong owner/device admission, non-admin access or missing mutation guard |
| 404 | Missing or deliberately undisclosed resource; foreign IDs must not be treated as permission to recreate it |
| 405 | Unsupported operation in native identity dispatch; other routers can use 400/404 instead |
| 409 | Stale revision/cursor/selection, idempotency conflict, wrong upload offset or incomplete finalization |
| 410 | Expired upload staging or archive; public expired/burned grabs generally use 404 instead |
| 411 | Required document Content-Length absent |
| 412 | Failed native Files revision precondition |
| 413 | Body/chunk/document/preview too large for that operation |
| 416 | Invalid or unsupported byte range |
| 422 | Upload/document checksum or length verification failure where specified |
| 423 | Vault locked in parts/recovery and certain newer native handlers |
| 429 | Rate or upload admission pressure; honor `Retry-After` |
| 500 / 503 | Internal or temporary service/storage failure; native errors may include retry hints |
| 507 | Physical storage/quota admission failure on upload/native write routes |

These are route-specific mappings, not a promise that every handler uses all
statuses consistently. In particular, generic Desk `apiError` defaults to 400;
do not assume every missing Library path is 404 or every generic PUT quota error
is 507. Browser uploads have the explicit mappings in their section.

Keep retry identity stable. After a lost ordered PATCH response, query the server
offset before resending. For parts PUT, retry the exact same index/hash/length.
For a mint with `Idempotency-Key`, resend the same key and specification. Do not
blindly retry an unkeyed mint or a Share download: they can create another grab
or consume another transfer. Vault lock is not evidence a credential was revoked.

## Health and discovery

| Method | Path | Access | Request and response |
| --- | --- | --- | --- |
| GET | `/live` | Public, all listeners | 200 `{"ok":true,"process":"live"}` |
| GET | `/ready` | Public, all listeners | 200 readiness; application storage middleware reports `{"ok":true,"storage":"ready"}` or 503 storage unavailable |
| GET | `/api/status` | Public Desk | `{setup,users,authenticated,quota?}`; authenticated owners also receive `username,admin,unlocked` |
| GET | `/.well-known/weazlcloud` | Public Desk | `{instance_id,contract_version:1,capabilities_path}` |
| GET | `/api/v1/mobile/capabilities` | session / device | Current capability/limits projection described below |
| GET | `/api/v1/mobile/profile` | session / device | Same capability projection, not a separate editable-profile API |
| GET | `/api/v1/mobile/status` | session / device | Same capability projection |

Mobile discovery includes `owner_id`, profile `username/full_name`,
`vault_unlocked`, feature flags, transports and limits. Bearer responses add
`device_id,generation,grants,expires_at`; cookie responses add
`authentication: account_session`. Discover active part/receive/finalization and
polling limits here instead of hard-coding host-dependent concurrency.
`instance_id` survives restart and whole-volume restoration. It helps identify
connection aliases but does not replace TLS or approved-origin checks.

## Accounts, sessions and vaults

All mutation rows require the Desk guard. Account request bodies are bounded
at 8 KiB. Usernames are 3–32 characters: first alphanumeric, then letters,
numbers, `.`, `_` or `-`. Password validation is server-side; do not substitute
username validity for credential validation.

| Method | Path | Access | Request → success |
| --- | --- | --- | --- |
| POST | `/api/bootstrap` | Public, empty node only | `{username,password,vault_passphrase?,confirm?}` → 201 `{id,username,admin:true}` plus cookie; existing setup returns 409 |
| POST | `/api/login` | Public | `{username,password}` → 200 `{id,username,admin}` plus cookie |
| POST | `/api/logout` | session | `{}` → 200 `{"ok":"logged out"}`, clears cookie |
| GET | `/api/me` | session | `{id,username,full_name,admin,unlocked}` |
| POST | `/api/access-requests` | Public | `{username,note}` → 201 `{id,username,status,created_at}` |
| POST | `/api/account-setup` | Setup invitation | `{id,token,username,password,vault_passphrase?,confirm?}` → 201 account identity; then log in |
| POST | `/api/settings` | session | `{full_name,current_password?,new_password?}` → `{id,username,full_name}` |
| POST | `/api/unlock` | session | `{passphrase}` → `{"ok":"unlocked"}` |
| POST | `/api/lock` | session | `{}` → `{"ok":"locked"}`; stops private work and cancels owner Takeout work |
| POST | `/api/vault/rekey` | session | `{current,next,confirm}` → `{"ok":"vault rekeyed"}` |
| POST | `/api/kit` | session + passphrase | `{passphrase}` → `{"ok":"kit"}` after creating/verifying a recovery kit on the server; does not stream a kit download |

Bootstrap creates the first administrator and, where present, adopts a checked
legacy single-owner vault. `vault_passphrase` defaults to account password and
`confirm` defaults to that passphrase when omitted. Setup/user creation use the
same defaults. Bootstrap/login do not replace the explicit unlock flow. Account
password changes invalidate device authorization; vault rekey changes the vault
passphrase and has separate semantics. Login/access-request/unlock attempts are
rate limited; a limit response has `Retry-After: 60`.

## Administration, settings and capacity

| Method | Path | Access | Request → success |
| --- | --- | --- | --- |
| GET | `/api/access-requests` | admin | `{requests:[{id,username,note,status,created_at,approved_at,consumed_at}]}` |
| POST | `/api/access-requests/approve` | admin | `{id}` → `{request,setup_token}`; deliver the one-time invitation privately |
| POST | `/api/access-requests/reject` | admin | `{id}` → `{"ok":"rejected"}` |
| POST | `/api/users` | admin | Bootstrap-shaped body → 201 new non-admin `{id,username,admin:false}` |
| GET | `/api/admin/users` | admin | `{users:[{id,username,full_name?,admin,disabled,disable_pending,deleting,delete_error?,disable_error?}]}` |
| POST | `/api/admin/users/disable` | admin | `{id,disabled}` → `{ok:true,disabled}`; conflicts return 409 |
| POST | `/api/admin/users/delete` | admin | `{id,confirm_username}` → `{status:"deleted"}` or 202 `{status:"deleting",error,account}` while cleanup is pending |
| GET | `/api/admin/maintenance` | admin | `{jobs:[...]}` idle maintenance status; 503 if unavailable |
| GET | `/api/node` | admin | `{hostname}` canonical public grab DNS hostname |
| POST | `/api/node` | admin | `{hostname}` → `{hostname}`; DNS host only, no path/credentials/IP address; persists an HTTPS base |
| GET | `/api/places` | session | `{grab,drive,token?}`; legacy drive token projection only while unlocked |
| POST | `/api/places` | session | `{grab,drive,token?}` → saved places object; owner cannot replace the administrator's grab base |
| GET | `/api/quota` | session / S:R | Capacity object; unlocked vault adds Library accounting |
| GET | `/api/qr` | session | Query `url` must be absolute HTTPS; returns a 320px PNG, `Cache-Control: no-store` |

Disabling/deleting revokes access and drains owner work; delete has a 30-second
request deadline and may continue with `deleting` status. Inspect the admin user
list for pending/error state. These operations are not equivalent to trashing a
file. Confirmation must match the target username.

Quota returns `capacity,used,limit,reserved,percent,users,available_bytes`; values
other than percent/users are bytes. When unlocked, fields can include
`logical_bytes,unique_bytes,trash_bytes,trash_count,dedupe_percent`, plus
`dedupe_scope` and `shared_*_allocated_bytes` for shared storage. This projection
is not an API to set per-account quotas. `/api/places` does not change WebDAV's
account Basic authentication contract.

## Library files, previews and events

Private Library operations require an unlocked owner vault. `FileView` is
`{path,folder?,size,mtime}`. Browser lists deliberately do not return the native
stable-ID DTO; use native Files sync to discover IDs for ID-based APIs.

| Method | Path | Access | Request → success |
| --- | --- | --- | --- |
| GET | `/api/library` | session / F:R | Without nonempty `path`: `{files:[FileView...]}` for the whole library; with `path`: streams file bytes |
| PUT | `/api/library` | session / F:W | Query `path`, raw binary body → 200 `FileView`; ordinary mutable Library write |
| DELETE | `/api/library` | session / F:W | Query `path` → `{"ok":"deleted"}`, moves file/folder to Trash |
| GET | `/api/library/page` | session / F:R | Folder query below → `{files,next_cursor,generation}` |
| GET | `/api/library/search/page` | session / F:R | Search query below → same page envelope |
| POST | `/api/library/folder` | session / F:W | `{path}` → 201 `{path,folder:true}` |
| POST | `/api/library/rename` | session / F:W | `{from,to}` → `{"ok":"renamed"}`; also used to move |
| POST | `/api/library/copy` | session / F:R + F:W | `{from,to}` → `{"ok":"copied"}` |
| GET | `/api/library/thumbnail` | session / F:R | Query `path,id?,size?` → image bytes; photo `id` overrides path, size defaults to 320 and clamps to 96–1280; cache private for 3600 seconds |
| GET | `/api/library/capability` | session / F:R | Query `path` → `{path,kind,label,content_type}`; kind such as `thumbnail,media,text,pdf,model,download` |
| GET | `/api/library/music` | session / F:R | Query `path` → optional string fields `title,artist,album,album_artist,genre,year,track,artwork` (artwork is a data URL); missing tags do not imply a corrupt original |
| GET | `/api/library/events` | session / F:R | Server-sent events for owner Library changes |

Folder query: `path` (empty root), `limit` (default 100, cap 200), `cursor`,
`sort=name|modified|size|type`, `desc=1` for descending. Search query adds
`q` (case-insensitive substring or `*`/`?` pattern), `scope` (optional subtree),
`type=all|image|video|audio|document|archive`, `date=all|7d|30d|365d`,
`size=all|small|medium|large`. Small means under 1 MiB; medium is 1 to under
100 MiB; large is at least 100 MiB. Search `q` is bounded to 256 bytes.
Continue with identical query/sort and the returned cursor. Cursors bind catalog
generation; mutation can return **400** `folder changed; reload the view` or
`search changed; reload results`. Restart that listing, preserving the existing
visible page until the replacement succeeds.

Content GET defaults to attachment; nonempty `inline` requests inline disposition.
Nonempty `preview` requests the supported preview form. Native image/audio/video/PDF
preview streams original-format bytes; other rendered previews have a 64 MiB
source bound. Original downloads have no corresponding preview-size limit.
Library GET accepts one `Range: bytes=...` and returns 206, or 416 with
`Content-Range: bytes */<size>`. This path API does not promise the native Files
ETag/precondition contract or an explicit HEAD route. Midstream errors terminate
the content stream. See [formats and limits](operations-reference.md).

```sh
curl --fail-with-body -b cookies.txt -H 'X-Weazl-Desk: 1' \
  --upload-file ./song.flac \
  "$cloud_origin/api/library?path=Music%2Fsong.flac"
curl --fail-with-body -b cookies.txt --get --data-urlencode 'path=Music/song.flac' \
  --output ./downloaded-song.flac "$cloud_origin/api/library"
```

### Change events and responsive clients

Events use `Content-Type: text/event-stream`, `Cache-Control: no-cache, no-store`,
`X-Accel-Buffering: no`, and a 20-second comment heartbeat. An example event is:

```text
id: 42
event: library
data: {"kind":"put","paths":["Music/song.flac"]}

```

Treat `kind/paths` as an invalidation hint, not a durable replication journal.
The handler does not replay `Last-Event-ID`; re-list after reconnect. Native
Files/Photos sync provides durable checkpoints. Cookie clients can use
`EventSource`; bearer clients need a streaming client that can supply headers.

Upload completion and its SSE notification can describe the same mutation.
Coalesce them, keep foreground navigation independent, preserve focused controls
and visible rows, and replace state only after a complete current-scope response.
The deployed browser now follows that policy and retains loaded pages and
hydrated previews during uploads. See [the fix and verification](library-upload-refresh-2026-10-24.md).

## Browser resumable uploads

These routes require an owner cookie and unlocked vault. **Device bearer tokens
cannot use `/api/uploads`, including tokens with `files:write`.** Use native
photo/backup transports or direct scoped Library PUT instead.

| Method | Path | Request → success |
| --- | --- | --- |
| GET | `/api/uploads` | Bare array of owner `SessionView` objects |
| POST | `/api/uploads` | `{path,size,hash?}` → 201 `SessionView`; hash is full-file SHA-256 in hex |
| GET | `/api/uploads/{id}` | Current `SessionView` |
| PATCH | `/api/uploads/{id}` | Raw chunk, `Upload-Offset`, `Upload-Chunk-SHA256`, known Content-Length → updated `SessionView` |
| POST | `/api/uploads/{id}/finalize` | Empty body or `{}` → `{upload:SessionView,file:FileView}` |
| DELETE | `/api/uploads/{id}` | Cancel → `{status:"cancelled"}` |

`SessionView` fields: `id,path,size,offset,hash?,chunk_hashes?,status,created_at,
updated_at`. Status progresses through `uploading`, `ready`, `finalizing`,
`complete`. `ready` means all bytes staged, not a file committed to the Library.
Create is not keyed/idempotent for this browser route: retain its returned ID.
Finalization is retryable for the same session; on a completed-session replay,
the `upload` receipt is authoritative and the auxiliary `file` object need not
be populated again.

Each PATCH is at most 16 MiB; the browser currently uses 8 MiB chunks on three
independent file rails. Offset is a nonnegative decimal byte offset. Supply the
chunk's SHA-256, not the full-file checksum, in `Upload-Chunk-SHA256`. A conflicting
offset returns 409 with `{error,offset:<server-accepted-offset>}`. Invalid chunk
hash/length verification returns 422; oversized chunk 413; incomplete finalize
409; missing session 404; expired session 410; storage admission 507; lock 401.
Unfinished staging expires after 24 hours of inactivity and is periodically swept.
New staging is encrypted; preserved legacy staging can still drain/resume.

Example for a small file fitting one chunk (requires `jq`):

```sh
upload_path='Music/example.flac'
upload_size=$(wc -c < ./example.flac)
upload_hash=$(sha256sum ./example.flac | cut -d ' ' -f 1)
jq -n --arg path "$upload_path" --argjson size "$upload_size" --arg hash "$upload_hash" \
  '{path:$path,size:$size,hash:$hash}' > upload.json
upload_id=$(curl --fail-with-body -b cookies.txt -H 'X-Weazl-Desk: 1' \
  -H 'Content-Type: application/json' --data-binary @upload.json \
  "$cloud_origin/api/uploads" | jq -er .id)
curl --fail-with-body -b cookies.txt -X PATCH -H 'X-Weazl-Desk: 1' \
  -H 'Upload-Offset: 0' -H "Upload-Chunk-SHA256: $upload_hash" \
  -H 'Content-Type: application/octet-stream' --data-binary @example.flac \
  "$cloud_origin/api/uploads/$upload_id"
curl --fail-with-body -b cookies.txt -X POST -H 'X-Weazl-Desk: 1' \
  "$cloud_origin/api/uploads/$upload_id/finalize"
```

For larger files, send bounded consecutive chunks and confirm each returned
offset. A network timeout does not mean the server accepted zero bytes. A server
`mkdir /data/uploads/...: permission denied` is an operator filesystem problem,
not a filename/codec/size error; see [upload permissions](docker-compose.md#upload-staging-permissions).

## Trash and owner ZIP downloads

All rows require an unlocked vault. Trash retains originals for 30 days;
restoring can conflict with an occupied destination. Trash cleanup removes only
expired items, not all currently retained Trash.

| Method | Path | Access | Request → success |
| --- | --- | --- | --- |
| GET | `/api/trash` | session / F:R | `{items:[{id,revision,path,folder,size,mtime,deleted_at?}],retention_days}` |
| POST | `/api/trash/restore` | session / F:W | `{path}` → `{"ok":"restored"}` |
| DELETE | `/api/trash` | session / F:W | `{ok:true,reclaimed_bytes}` after expired-item cleanup |
| POST | `/api/library/archive` | session / F:R | `{paths:[...]}` (body ≤1 MiB) → 202 archive job |
| GET | `/api/library/archive` | session / F:R | Query `id`; add `download=1` to stream when ready; otherwise returns job |
| DELETE | `/api/library/archive` | session / F:R | Query `id` → `{"ok":"cancelled"}`; unavailable/non-cancellable job 404 |

Archive job: `{id,status,files,bytes,created_at,expires_at,error?}`. Poll status
until `ready`; `download=1` before readiness returns JSON rather than ZIP bytes.
Ready ZIPs expire 90 minutes after preparation and support range reads through
HTTP content serving. Jobs and output are encrypted, with restart recovery.
These are owner downloads, not public capsule downloads. Photos exports use the
selection-action route and `/api/v1/photos/archives` instead.

## Takeout imports

Only the configured `WEAZLCLOUD_IMPORT_OWNER` session can use the staged import
mount; device tokens cannot. Starting requires an unlocked vault. ZIP source
names refer to files already placed in `WEAZLCLOUD_IMPORT_DIR`, not uploaded
through this API. Regular Library uploads of ZIP files do not import them.

| Method | Path | Request → success |
| --- | --- | --- |
| GET | `/api/takeout` | `{archives:[{name,size}],jobs:[{name,status,summary,error?,updated_at}]}` |
| POST | `/api/takeout` | `{name,skip_corrupt?}` → 202 `{name,summary,status:"running"}` |
| DELETE | `/api/takeout` | `{name}` → `{"ok":"cancelled"}` for a running job |

A second running import returns 409. Terminal status is `complete` or `failed`;
inspect summary/error counts rather than assuming every archive entry succeeded.
Retries use the import audit to retain already verified entries. Cancellation
and vault lock stop work without automatically deleting source ZIPs or committed
Library files. See [staging and recovery](google-takeout-import-2026-09-24.md).

## Device enrollment and credentials

| Method | Path | Access | Request → success |
| --- | --- | --- | --- |
| GET | `/api/v1/devices` | session / device | `{devices:[...]}`; cookie sees owner devices, bearer sees itself |
| POST | `/api/v1/devices` | session, unlocked | `{name,scopes?}` → 201 `{device,token,scopes}`; legacy enrollment also includes `scope` |
| GET | `/api/v1/devices/{id}` | session / self device | `{device,grants}` |
| POST | `/api/v1/devices/{id}/rotate` | self device bearer | `{operation_id,expected_generation,replacement_token}` → `{device,generation}` |
| POST | `/api/v1/devices/{id}/reauthorize` | session, unlocked | `{replacement_token,scopes?}` → `{device,grants}` |
| POST | `/api/v1/devices/{id}/revoke` | session / self device | `{}` → `{status:"revoked"}` |
| POST | `/api/v1/devices/revoke` | session / self device | Legacy body `{id}` → revoked result |

Public device records include stable `id,owner_id,name`, granted scopes,
`generation,authorization_version`, created/expiry times and revoked state, but
never stored secret hashes. At most 32 active devices; tokens last 90 days.
Persist a client-generated 64-hex replacement token **before** rotation. An exact
rotation retry returns the committed generation; changing operation/generation/
secret conflicts with 409. Previous token grace is at most 15 minutes and cannot
start another rotation. Reauthorization preserves device identity; omitted scopes
preserve existing grants. Revoked/expired authorization cannot resurrect itself.

## Photos browsing, editing and organization

Unless stated otherwise, photo reads accept session / `P:R`; mutations accept
session / `P:W`; an unlocked vault is required. Legacy Photos compatibility is
route-specific. Hidden is an explicit Photos visibility context within the same
owner vault, not a second password boundary. Use `hidden=1` where supported or
`mode=hidden` for the timeline; normal queries cannot reveal Hidden originals,
thumbnails, covers or memberships by guessing an ID. Owner Library access still
exposes the underlying files.

### Metadata, content and navigation

| Method | Path | Inputs → response |
| --- | --- | --- |
| GET | `/api/v1/photos/capabilities` | Available while locked; reports vault state, roots, features and limits; locked root list is empty |
| GET | `/api/v1/photos` | `limit,cursor,album,mode,date,around` → timeline page |
| GET | `/api/v1/photos/search` | `q,type,camera,from,to,favorite,archived,hidden,unknown,outside_albums,album,limit,cursor` → photo page |
| GET | `/api/v1/photos/dates` | Same rail scope, optional `month=YYYY-MM` → month/day counts, ranks, unknown totals and generation |
| GET | `/api/v1/photos/seek` | Rail scope plus `at`, `rank`, `around` or `cursor` → bounded page with anchor/position |
| GET | `/api/v1/photos/assets/{id}` | Optional `hidden=1` → photo item |
| POST | `/api/v1/photos/assets/{id}` | Optional query `hidden=1`; partial body below → updated item |
| GET | `/api/v1/photos/assets/{id}/thumbnail` | `size=96..1280` (default 320), `hidden=1` when needed → image bytes |
| GET, HEAD | `/api/v1/photos/assets/{id}/original` | `hidden=1`, optional `download=1` → immutable original, Range/ETag support |
| GET, HEAD | `/api/v1/photos/assets/{id}/motion` | **Session only**; `hidden=1` when needed → prepared MP4 Live Photo motion; no device route grant currently |
| POST | `/api/v1/photos/lookup` | `P:R` despite POST; `{items:[{sha256,size}],include_hidden?}` → content lookup results |
| GET | `/api/photos` | Browser compatibility route for timeline handler |
| GET | `/api/photos/albums` | Browser compatibility album list |
| GET | `/api/photos/dates` | Browser legacy date summary; `hidden=1`; not the full v1 seek/dates scope |

Timeline `mode`: `all,recent,favorites,archived,hidden`. `date` accepts a year,
month, day or `unknown`. Pages default to 100, max 200, with `items,generation,
next_cursor,previous_cursor` and position/total fields where provided. IDs and
revisions, not paths, identify assets. Items can contain `path,size,media_type,
mtime,imported_at,captured_at,capture_offset_minutes,capture_source,width,height,
favorite,archived,caption,components,parent_asset_id,preview_identity,thumbhash`.
Fields vary by media/metadata availability; exact DTOs are in the Photos spec.

Search supports `*`/`?`, `type=image|video`, JPEG EXIF camera text, and inclusive
capture calendar dates. Boolean query flags use `1`. `outside_albums=1` cannot
be combined with an album filter. Rail `dates/seek` additionally use `mode,date,
search=1`; seek destinations are `at=YYYY-MM-DD|unknown`, `rank=N`, `around=<id>`
or a returned cursor. A seek does not add a destination-day filter. Continue
with the same scope. Stale Photos cursors normally return 409; malformed ones
400. Unknown capture dates are never synthesized from mtime/import time.

Asset edit body fields are optional: `favorite`, `archived`, `caption`,
`captured_at` (RFC 3339 or empty for unknown), `offset_known`, `rotation`
(0/90/180/270). Originals remain unchanged; rotation alters derivatives.
Captures must be in the supported 1900–2100 range. Timeline archive state retains
album membership and originals.

Original ETag is `"<id>-<revision>"`. Exact `If-None-Match` can return 304;
single ranges and exact `If-Range` are supported. This is narrower than native
Files preconditions. HEAD has no body but this handler can still honor Range
in its status/headers. Content responses are private/no-store.

Lookup accepts 1–200 hash/positive-size queries in ≤64 KiB; results preserve
input order and include `exists,matches,match_count,has_more_matches` plus hash
and size. Each match has `asset_id,revision,component_id,component_asset_id,hidden,
archived`; at most 20 matches are returned. Query both components of a Live Photo
and verify the same parent. Lookup is an observation, **not a durable backup
receipt or reservation**. See [lookup contract](photo-content-lookup.md).

### Albums, folders, selections and exports

| Method | Path | Inputs → response |
| --- | --- | --- |
| GET | `/api/v1/photos/albums` | `hidden=1` for Hidden context → `{albums:[...]}`; at most 200 member IDs per summary |
| POST | `/api/v1/photos/albums` | `{action:"save"\|"members"\|"delete",...}` → album header or `{status:"deleted"}` |
| GET | `/api/v1/photos/albums/memberships` | `id,cursor,limit,hidden` → bounded member IDs, complete paging |
| POST | `/api/v1/photos/folders` | `{path,hidden}` → `{path,hidden}`; Hidden inherits through parent folders |
| GET | `/api/v1/photos/trash` | `hidden=1` for explicit Hidden trash → `{items,retention_days}` |
| POST | `/api/v1/photos/trash/restore` | `{id}`, optional query `hidden=1` → `{"ok":"restored"}`; restore deleted parents before children |
| POST | `/api/v1/photos/selections` | `P:R`; explicit `ids` or `{filter,mode?,date?,search?}` → 201 `{id,count,hidden,expires_at}` |
| POST | `/api/v1/photos/selection-actions` | `P:R` + `P:W`; action body below → action result or accepted ZIP job |
| GET | `/api/v1/photos/archives` | `P:R`; query `id`, optional `download=1` → photo-scoped job or ready ZIP |
| DELETE | `/api/v1/photos/archives` | `P:W`; query `id` → cancel unfinished photo ZIP |

Album save accepts `id?,revision,title,description,cover_id,position,asset_ids,parent_id?`
and `hidden`; a new album can use `selection_id` for initial membership. Member
changes use `id,revision,add_ids,remove_ids`; delete uses `id,revision`. Updates
are optimistic; stale revision is 409. Deleting an album does not delete media.
Custom timeline album filters use `album:<id>`. Enumerate `/albums/memberships`
(max 200/page) rather than treating bounded summary `asset_ids` as a full export.

Selection tokens bind owner, visibility and revisions, last 90 minutes and can
represent up to 100,000 primary IDs retained server-side. Action body:
`{selection_id,hidden,action,confirm_hidden?,album_id?,revision?}`. Actions:
`archive` (prepare ZIP), `add_album`, `remove_album`, `delete`, `hide`, `unhide`,
`set_archived`, `unarchive`. `set_archived` differs from ZIP `archive`. Hidden
exports need `confirm_hidden:true`. Album actions need `album_id/revision`.
Archive actions return 202 with the ZIP job; album actions return 200 with the album header; visibility and delete actions return 200 with `{updated:count}` and `{deleted:count}` respectively.
Stale/expired selections return 409. An inherited Hidden folder must be unhidden
before its child can become normally visible. Photo archive routes reject generic
Library archive IDs, even for the same owner.

### Maintenance and Live Photos

| Method | Path | Access and contract |
| --- | --- | --- |
| GET, POST | `/api/photos/preparation` | Session only; GET status, POST `{action:"start"\|"resume"\|"pause"\|"retry"}` → 200 preparation status |
| GET | `/api/v1/photos/metadata-jobs` | Session or legacy Photos credential; current repair status; `report=1&cursor=N` gives `{entries,next_cursor}` in pages of 100 |
| POST | `/api/v1/photos/metadata-jobs` | Same; `{action,root?,dry_run?,sidecars_only?}` → repair status; action `start,dry-run,pause,resume,retry` |
| GET | `/api/v1/photos/duplicates` | Session or legacy Photos credential; `cursor,hidden` → up to 20 exact-match groups/page |
| POST | `/api/v1/photos/duplicates` | Same; `{id}`, optional query `hidden=1` → `{status:"preferred"}`; no automatic media deletion |
| GET | `/api/v1/photos/live-jobs` | Session only; optional `report=1` → Live Photo repair job/report |
| POST | `/api/v1/photos/live-jobs` | Session only; `{action}` → 202 job; actions `start,dry-run,pause,resume,retry` |
| POST | `/api/v1/photos/live-pair` | Session only; `{still_id,motion_id,still_revision,motion_revision,hidden,unlink}` → updated logical photo |
| GET | `/api/v1/photos/failures` | Session only; `start` nonnegative report offset → failure report |

Explicit scoped tokens cannot enroll `photos:v1`; the duplicate/metadata grants
are compatibility-only rather than automatically granted by `photos:write`.
Preparation fills 320/1280 preview bundles; `ready` and `bundle_ready` distinguish
retained variants. Jobs continue without a browser while the vault is unlocked;
manual pause survives restart. Original storage success is independent of preview
failure. [Operations](operations-reference.md) covers limits and recovery.

## Photo and backup upload transports

Photos and recurring backups use logical coordinator receipts, not generic
browser sessions. Photos routes allow session / `P:W`; backups allow session /
`B:W` + `F:W`. Cookie callers must explicitly select an enrolled owner device
(body `device_id` on create; query `device_id` for relevant follow-up operations).
Bearer requests are always bound to their authenticated device. All require an
unlocked vault and mutations require the Desk guard.

### Create specifications

Photo create fields: `device_id?`, `device_asset_id`, `source_revision` (default
`"1"`), `root_id` (default `root:photos`), `components`, `album_ids?` (≤100),
`captured_at?`, `offset_known?`, `hidden?`, `original_mode?`, `source_namespace?`,
`source_asset_id?`, `source_mapping_revision?`, `transport?`,
`commit_when_complete?`. Components are one `original` and optional `motion`,
each `{id,filename,media_type?,size,sha256}`; Photos sizes must be positive.
Legacy flat `{filename,size,sha256}` supplies one original. Initial Hidden state
and the pair publish atomically. Valid format sniffing does not guarantee every
frame/preview decodes. `original_mode:"opaque-original-v1"` preserves verified
opaque bytes without claiming preview support.

Photo retry identity is `(owner,device,device_asset_id,source_revision)`.
Identical normalized create recovers the receipt; changed immutable metadata
conflicts with 409. New content needs a new source revision. Native source IDs
can use the framed SHA-256 derivation documented with a byte-exact fixture in
[mobile API](mobile-api.md#photo-identity-originals-and-organization).
Placement is under a capability-advertised Photos root, in
`{root}/Mobile/{device}/{deviceAsset}/{sourceRevision}/{filename}`.

Backup specification is described in [recurring backups](#recurring-backups).
Choose `transport:"parts-v1"` for independent encrypted parts and optionally
`commit_when_complete:true` for autonomous finalization. Otherwise Photos uses
legacy ordered PATCH and backups use `transport:"sequential"`. They are distinct
wire contracts, even when backed by the same staging primitives.

### Endpoint inventory

| Method | Path | Operation |
| --- | --- | --- |
| POST | `/api/v1/photos/uploads` | Create/recover photo logical receipt, optionally parts transfer |
| GET, DELETE | `/api/v1/photos/uploads/{id}` | Status or durable cancellation |
| PATCH | `/api/v1/photos/uploads/{id}` | Append to old single-original receipts only; new receipts use `/components/original` |
| PATCH | `/api/v1/photos/uploads/{id}/components/{component}` | Ordered append to `original` or `motion` |
| PUT | `/api/v1/photos/uploads/{id}/components/{component}/parts/{index}` | Independent parts PUT |
| GET | `/api/v1/photos/uploads/{id}/parts` | Missing part indices |
| POST | `/api/v1/photos/uploads/{id}/retry` | Requeue complete failed/non-stored parts work |
| POST | `/api/v1/photos/uploads/{id}/finalize` | Ordered synchronous commit, or parts queue admission |
| POST | `/api/v1/photos/uploads/status` | Batch status for parts Photos only |
| POST | `/api/v1/backups/uploads` | Create/recover backup logical receipt, optionally parts transfer |
| GET, DELETE | `/api/v1/backups/uploads/{id}` | Status or cancellation |
| PATCH | `/api/v1/backups/uploads/{id}` | Sequential ordered append |
| PUT | `/api/v1/backups/uploads/{id}/components/{component}/parts/{index}` | Backup parts; component must be `original` |
| GET | `/api/v1/backups/uploads/{id}/parts` | Missing part indices |
| POST | `/api/v1/backups/uploads/{id}/retry` | Requeue complete parts work |
| POST | `/api/v1/backups/uploads/{id}/finalize` | Sequential commit or parts queue admission |

Ordered PATCH uses known Content-Length, `Upload-Offset` and
`Upload-Chunk-SHA256`, at most 16 MiB, consecutive offsets. Photos GET returns
logical receipt/component state; finalize verifies all components before atomic
publication and returns `status:"stored"` separately from
`processing_state: pending|processing|ready|failed`. Creation capture metadata,
or the first persisted finalize capture metadata if omitted at create, wins on
retry. Parts finalize cannot change immutable create metadata.

Parts PUT uses `X-Weazl-SHA256`, **not** `Upload-Chunk-SHA256`. Index is zero-based;
offset is `index * 16777216`. Every nonfinal part is exactly 16 MiB and the final
part is the exact remaining size. Send known Content-Length and file-backed raw
bytes. Parts can arrive out of order. Matching hash/index/length repeats are
idempotent; changing an accepted part conflicts. Full-component size/hash is
verified again before stored-original publication. Zero-byte backup files need
no data parts; empty-folder backup create commits synchronously.

Parts create returns 201 `{receipt,transfer}`; stored-create replay returns
200 `{receipt}`; empty folder create returns 201 `{receipt}`. Status, successful
PUT, retry and finalize return 200 `{transfer}`. Transfer fields are
`id,transport,status,components,part_size,error_code?,result?,updated_at,expires_at`.
Components contain `id,size,sha256,received_parts,received_bytes`. States:
`uploading,queued,verifying,stored,failed,cancelled`. Only `stored` with the
coordinator receipt in `result` proves stored originals; queued/verifying have
`Retry-After: 2`. Background errors include `stale_revision,source_inactive,
insufficient_storage,device_authorization_required,vault_locked,commit_failed`.

Missing-part GET accepts `component=original|motion`, `cursor` (index default 0),
`limit` 1–200/default 200; response `{missing:[indices],next,has_more}`. Follow
`next` while `has_more`, including empty pages. Cancellation is terminal for that
intent and returns `{status:"cancelled",transfer}`. If an original had already
published, cancellation instead recovers stored state and returns 409
`already_stored`; it never deletes the original. There is no implemented `/receipt`
alias: get status at the logical upload ID.

Batched Photos status accepts ≤16 KiB `{upload_ids:[1..100 unique hex IDs]}` and
returns ordered `{items:[{upload_id,transfer? ,code?}]}`. Missing/foreign/non-photo
IDs share `not_found`; per-item codes also include `staging_expired,staging_corrupt,
status_unavailable`. Pending items set `Retry-After: 5`. Poll around every five
seconds, back off unchanged work, and keep polling independent of upload PUTs.
There is no backup batch-status endpoint. Discover support and concurrency using
mobile capabilities. Receive/backlog pressure is 429 `upload_busy`, Retry-After 5;
physical storage denial is 507. Preserve accepted work while backing off.

Inactive unfinished staging expires after 24 hours. Polling does not extend its
life. Identical intent can restart an expired stage after fresh authorization and
quota checks, but accepted bytes must be retransmitted; durable stored receipts
remain. Rotation/reauthorization can re-admit identical unfinished intents under
the guarded rules in the [full mobile transport contract](mobile-api.md#independent-parts-and-server-jobs).

## Photo synchronization and source recovery

| Method | Path | Access | Contract |
| --- | --- | --- | --- |
| GET | `/api/v1/photos/sync` | session / P:R | `cursor,limit,hidden,resume` as applicable → snapshot items or delta changes/checkpoint |
| POST | `/api/v1/photos/sync/checkpoint` | Device P:R | `{checkpoint,hidden}` → checkpoint acknowledgment |
| GET | `/api/v1/photos/collections` | session / P:R | `cursor,limit,hidden` → schema-2 organization snapshot or deltas |
| POST | `/api/v1/photos/collections` | session / P:W | `{action,folder?,album?,parent_id?}`; `save-folder,delete-folder,save-album` |
| POST | `/api/v1/photos/source-collections` | session with device / P:W | `{device_id?,operations:[...]}` → ordered source organization outcomes |
| POST | `/api/v1/photos/source-memberships` | session with device / P:W | Same batch envelope; album membership additions |
| POST | `/api/v1/photos/source-collections/lookup` | session / P:R | `{source_ids,namespace?,limit?,cursor?}` → owner-wide recovery matches |
| POST | `/api/v1/photos/source-collections/recover` | session with device / P:W | Guarded adoption body below → `{mapping}` |

Photo sync captures a baseline before snapshot enumeration. Exhaust snapshot
pages, then replay checkpoint deltas; apply tombstones and stable-ID revisions.
Album headers are bounded and require separate membership paging. Normal/Hidden
checkpoints are separate. Journal retention is 8,192 changes; expired/divergent
positions return 409 `resync_required`. Device checkpoint acknowledgment is only
for that device; `resume=1` requires a device bearer and requests its saved checkpoint. Snapshot replacement
must reconcile removed IDs, not append to an old cache.

Collections are virtual organization, separate from Library folders. Folder
fields: `id,parent_id?,title,position,revision`; album headers include `parent_id`.
Updates/deletion require current revisions; deleting a folder requires it to be
empty and never deletes originals. Parent must exist, nesting is bounded to 64
nodes including self, and cycles are rejected. For album updates, omitted parent
preserves it; explicit top-level `parent_id:""` moves to root. Collection snapshot
is `{schema,position,nodes,next?,has_more,checkpoint?}`; delta form uses
`changes,next_cursor?,checkpoint?,has_more`. Schema-2 collection cursors must not
be passed to legacy Photos sync.

Source batches contain 1–200 operations, at most 2 MiB. Each operation includes
`operation_id,namespace,source_id,source_revision,kind,parent_source_id?,title,
position,expected_revision,deleted?,add_ids?`; membership operations target
`kind:"album"`, with at most 200 asset IDs. Outcomes are `applied,conflict,invalid,
retryable,dependency-blocked` with `server_id/revision` where successful. Exact
successful operation replays are durable; failures do not consume the operation
ID. Deletion markers retain organization/originals; incomplete scans do not infer
server deletion. Children can precede parents within one batch.

Lookup takes 1–200 unique source IDs, optional namespace, and `limit` 1–200,
default 200, with an owner/query-bound cursor. Omitted namespace searches all.
Matches include `device_id,namespace,kind,source_id,server_id,source_revision,
server_revision,current_server_revision,target_exists`. Return all ambiguous
matches to the client; never choose one silently. Pages are live reads and
include mappings whose targets have been deleted. Continue the same request
with `next_cursor` while `has_more`.

Recovery adopts one explicitly selected existing target to the current device:
`{operation_id,from_device_id,from_namespace,from_source_id,from_source_revision,
from_server_revision,namespace,source_id,server_id,expected_server_revision,
device_id?}`. It verifies both old mapping and current target revision and never
recreates media or changes membership/hierarchy. Exact replay is durable;
missing old mapping is 404 `source_not_found`, stale target/mapping 409
`stale_revision`, occupied destination/operation 409 `mapping_conflict`, locked
vault 423. Use lookup values and the returned mapping revision for subsequent
source edits. Full recovery workflow: [mobile-source-recovery.md](mobile-source-recovery.md).

## Native Files and recurring backups

### Stable Files reads and sync

| Method | Path | Access | Contract |
| --- | --- | --- | --- |
| GET | `/api/v1/files/{id}` | session / F:R | `{id,revision,path,folder,size,mtime}` with owner-specific ETag |
| GET, HEAD | `/api/v1/files/{id}/content` | session / F:R | Captured immutable revision bytes; conditional/range behavior below |
| GET | `/api/v1/files/sync` | Device F:R | `cursor,prefix,limit,resume=1` → bounded snapshot/delta page |
| POST | `/api/v1/files/sync/checkpoint` | Device F:R | `{checkpoint,prefix?,device_id?}` → saved device checkpoint |

IDs are 32 lowercase hex characters. Folder metadata has no content stream.
Deleted/foreign IDs remain unavailable even if their old path is reused. Internal
`.weazl-*` entries are excluded. Stable-ID sync is the way to obtain IDs when
browser path listings only return `FileView`.

ETag hashes owner + stable ID + revision. Preconditions: strong `If-Match` list
or `*`, otherwise `If-Unmodified-Since`; then weak-capable `If-None-Match` list or
`*`, otherwise `If-Modified-Since`. Failed precondition returns 412; unchanged
GET/HEAD returns 304. GET accepts one byte range, including suffix/open-ended;
invalid/multiple ranges return 416 and full-size Content-Range. If-Range must
match the strong ETag or exact Last-Modified date; mismatch returns full 200.
HEAD ignores Range and reports full length. Replace a partial download if a
resume request returns 200; never append that full body to the partial file.
Lock/revoke may interrupt a stream. Offline copies cannot be remotely recalled.

Files sync limit is 1–200/default 100. Prefix matches itself/subtree and is
cursor-bound. Page fields: `items,changes?,next_cursor?,checkpoint?,has_more,
resync_required`; changes include `sequence,kind,id,deleted?,item?`. Out-of-prefix
moves become tombstones. Apply snapshot plus subsequent baseline deltas, including
empty pages with scan progress. Cursors bind owner/device/prefix, process boot,
vault session and journal position, with 24-hour expiry. Server restart, cursor
expiry or history divergence can require 409 resync; a valid stored checkpoint
may resume through `resume=1`. Only acknowledged delta/checkpoint tokens are
saved. See [offline reconciliation](mobile-api.md#stable-files-reads-and-offline-reconciliation).

### Recurring backups

| Method | Path | Access | Contract |
| --- | --- | --- | --- |
| GET | `/api/v1/backups/sources` | session with device / B:W + F:W | `device_id` for cookie, optional `after` → `{sources,next}`, max 200/current device |
| POST | `/api/v1/backups/sources` | Same | `{device_id?,source_id,destination_id,name}` → 201 source |
| PATCH | `/api/v1/backups/sources/{source_id}` | Same | `{status,expected_revision}`; `active,paused,detached` → source |

Source fields: `device_id,source_id,destination_id,name,status,revision`.
Destination must be an existing owner folder's stable entry ID, not its path.
Same registration replays; changed destination/name under the same identity
conflicts. Detached is terminal; pause/detach never deletes server originals.

Upload body at `/api/v1/backups/uploads`:
`{device_id?,source_id,source_item_id,source_revision,relative_path,kind?,size,
sha256,mtime,expected_entry_id?,expected_revision?,transport?,commit_when_complete?}`.
`filename` is a fallback for absent `relative_path`; `kind` defaults to `file`.
Folders require size 0 and empty hash; files can be empty but still require the
SHA-256 of their exact bytes. Intermediate and empty folders are retained.
Relative paths exclude absolute/backslash/dot/empty components, NUL and surrounding
whitespace, with component limit 255 bytes and total 4096 bytes. Source identity
strings are nonempty UTF-8, at most 1024 bytes and no NUL.

Receipt identity is `(owner,device,source_id,source_item_id,source_revision)`.
Coordinator response: `{id,receipt_id,status,spec,upload_id?,file?}`, where
`id == receipt_id`; use that logical ID for coordinator/parts routes.
`upload_id` is only underlying fallback staging identity. Replacing/moving a
mapped item requires both expected entry ID and revision. Publication checks
source, destination, parents, collisions and mapped entry atomically. A server
edit/move/delete or occupied destination is a conflict, not authorization to
overwrite. Missing phone items never imply server deletion. The complete upload
method list and wire transports are in [uploads](#photo-and-backup-upload-transports).

## Weazldocs

Dedicated document endpoints require device bearer authentication, an unlocked
vault and explicitly enrolled document scopes. Owner cookies alone do not
access this API. Other scopes and legacy Photos tokens cannot substitute for
`documents:*`. Owner UI/Library operations can still manage the same owner's
underlying files, so native clients must handle external conflicts.

| Method | Path | Scope | Contract |
| --- | --- | --- | --- |
| GET | `/api/v1/documents` | D:R | Query `path` (default `Weazldocs`), `cursor` → `{files:[FileView],next_cursor,generation}`; 200 items/page |
| POST | `/api/v1/documents/folder` | D:W | Query `path` → 201 `{path}`; creates missing parents, existing folder is idempotent |
| GET | `/api/v1/documents/content` | D:R | Query `path` → attachment bytes, `application/octet-stream` |
| PUT | `/api/v1/documents/content` | D:W | Query `path`, raw body, Content-Length and `X-Weazl-Content-SHA256` → 200 `FileView` |

Every path is canonical and case-sensitive, equal to `Weazldocs` or beneath
`Weazldocs/`; content must be below the root, not the root folder. Backslashes,
escapes and cleaned-but-not-identical paths are rejected. No mutable overwrite,
delete, rename, HEAD or dedicated Range contract is exposed here.

PUT is at most 256 MiB, verifies full bytes/length/hash on first publication,
and atomically creates missing parents. Existing same hash/size is an idempotent
success; different content/type is 409. Use a new application revision path for
new content. Missing length is 411; absent/malformed SHA-256 is 400
`checksum_required`; mismatch 422; excess size 413; quota denial 507.
Revocation during ingestion prevents publication. Clients should read back and
verify before accepting a revision. Expanded contract: [documents-api.md](documents-api.md).

## Owner-created grab links

Owner calls are on Desk; recipients call the separate Share listener. Minting
requires an unlocked vault and configured HTTPS grab base. Listing/revocation
can operate without exposing source plaintext. A capsule is a frozen payload,
not a live shared folder; later Library edits do not update it.

| Method | Path | Access | Contract |
| --- | --- | --- | --- |
| GET | `/api/capsules` | session / G:R | `{capsules:[...],base}` |
| POST | `/api/capsules` | session / F:R + G:W | `{path,kind?,gate,passphrase?,label?,expiry?,grabs?}` → capsule view plus canonical `url` |
| DELETE | `/api/capsules` | session / G:W | Query `id` → `{"ok":"revoked"}` |
| POST | `/api/v1/photos/grabs` | session / P:R + G:W | `{ids}` or `{selection_id}`, plus title/gate/expiry/grabs/Hidden confirmation → gallery capsule |
| GET | `/api/v1/grabs/operations` | session / G:R | Header `Idempotency-Key` → stored mint result or 202 `{operation_id,state:"minting"}` |

File `kind` is `file` (default) or `folder`. Gates: `open` or `passphrase`.
Expiry choices: `24h` (default), `3d`, `7d`; default transfer count is one.
Photo gallery body uses `title`, selected `ids` or `selection_id` (not both),
`gate,passphrase,expiry,grabs,hidden?,confirm_hidden?`; at most 10,000 originals.
Hidden material requires both Hidden flags. Album sharing should use a complete
selection, not bounded summary member IDs. Original bytes, previews and ZIPs
are frozen independently of source/vault-lock state.

For retry-safe mint, send `Idempotency-Key` (1–200 bytes, no surrounding whitespace,
CR/LF or NUL) on POST and operation lookup. JSON object key order/whitespace is
normalized; array order and options remain significant. Same owner/key/route/spec
replays the original result; a different spec/route conflicts with 409. Lookup
requires `grabs:read` even if the same token minted with `grabs:write`. Unkeyed
calls retain legacy non-idempotent behavior. Use returned URLs, not locally
constructed public-host aliases. Capsule views include identifiers, name/kind,
gate, expiry, transfer counts/status, size/member metadata; no unwrap material.

## Public Share API

These endpoints belong to the Share listener. They do not accept owner-cookie
or device-bearer authority. `{id}` is the 32-hex grab capability from the URL.
Do not confuse it with a photo asset ID. An unavailable, revoked, expired or
burned capability normally returns 404 `{error,status:"burned"}`; wrong phrase
401; storage failure 500. Failed phrase attempts are rate limited with 429 and
`Retry-After: 60`.

| Method | Path | Request → response |
| --- | --- | --- |
| GET | `/g/{id}` | HTML recipient page; does not spend a transfer |
| GET | `/g/{id}/meta` | `{id,name,kind,gate,size,expires,files,status:"live",left}`; no transfer spent |
| GET, POST | `/g/{id}/file` | Ordinary file/folder download; query `passphrase` or POST JSON `{passphrase}`; binary attachment and `X-Weazl-Grabs-Remaining` |
| POST | `/g/{id}/gallery` | `{passphrase}` or `{session}` → `{gallery,session}`; opens/renews gallery session |
| POST | `/g/{id}/preview/{member}` | `{session}` → preview image; no transfer spent |
| POST | `/g/{id}/motion/{member}` | `{session}` → prepared motion bytes; no transfer spent |
| POST | `/g/{id}/original/{member}` | `{session}` → original attachment; spends one transfer |
| POST | `/g/{id}/zip` | `{session,ids?}` → gallery ZIP; spends one transfer |
| POST | `/g/{id}/zip/jobs` | `{session,ids}` → 202 selected-ZIP job; no transfer spent |
| POST | `/g/{id}/zip/jobs/{job}` | `{session}` → job status; no transfer spent |
| POST | `/g/{id}/zip/jobs/{job}/download` | `{session}` → ready selected ZIP; spends one transfer |

Prefer POST for passphrases so they are not placed in URLs. Ordinary file/folder
`GET /file` is a consuming download; do not use it as a harmless probe. For a
gallery, `/file` delegates to the ZIP handler and therefore needs **POST** plus
a valid gallery session. Gallery operations accept JSON or form-encoded
`passphrase,session,ids` fields; JSON bodies are bounded to 1 MiB. Session is a
sealed 15-minute capsule-only credential. Members use new capability IDs, not
private catalog IDs or paths. Omitted ZIP IDs means the whole gallery.

Gallery originals/ZIPs require explicit POST; GET/HEAD do not admit a transfer.
Range on these downloads returns 416: reconnecting requires another explicit
transfer and spends another grab. Once admitted, even an interrupted download
counts. Final admission prevents new admissions, but an already admitted
transfer can finish; explicit revoke/account deletion cancels active transfers.
Previews omit EXIF/GPS while original downloads can retain it.

Selected-ZIP jobs have `queued,preparing,ready,failed` states, encrypted durable
output, and recover on polling after restart. There are up to 32 saved selections
per capsule and 64 active preparations per node. Re-requesting the same failed
selection can rebuild it without spending a download. Full gallery ZIPs are
already prepared at mint. Capsule outputs follow burn/expiry/revoke, not the
90-minute lifetime of ordinary owner ZIPs.

## WebDAV

Connect to the Drive listener root using the owner's account username/password
in HTTP Basic authentication. Use TLS outside trusted local development.
Cookie/device tokens and the legacy `/api/places` token are not this listener's
authentication mechanism. Successful Basic authentication uses node-backed vault
unlock; Basic auth is not accepted as a Desk API login substitute.

| Method | Path | Behavior |
| --- | --- | --- |
| OPTIONS | `/{path}` | Public method discovery; `DAV: 1, 2`, advertised Allow list below |
| GET, HEAD | `/{path}` | Owner file content/metadata |
| PUT | `/{path}` | Store file bytes under owner path |
| DELETE | `/{path}` | Delete through Library semantics |
| MKCOL | `/{path}` | Create collection/folder |
| COPY, MOVE | `/{path}` | WebDAV Destination/Overwrite behavior through the adapter |
| PROPFIND | `/{path}` | DAV XML properties/listing, Depth semantics |
| LOCK, UNLOCK | `/{path}` | Per-owner in-memory WebDAV locks |

Authenticate all methods other than OPTIONS and public health. Missing/invalid
credentials return 401 with `WWW-Authenticate: Basic realm="weazl"`; auth-rate
limits also use 401 with `Retry-After: 60`. Account disable/delete deny access;
node-backed vault-unlock failure is 503. DAV locks are not durable across restart.
The advertised method list is exactly `OPTIONS, GET, HEAD, PUT, DELETE, MKCOL,
COPY, MOVE, PROPFIND, LOCK, UNLOCK`; no separate application JSON body schema is
used for DAV methods. `/unlock`, `/index.html`, `/index.htm` and `/desk*` are
blocked on this listener. `/live` and `/ready` are reserved, not Library files.

```sh
# Curl prompts for the account password when only a username is provided.
curl --user alice -X PROPFIND -H 'Depth: 1' 'https://drive.example.test/'
curl --user alice --upload-file ./notes.txt 'https://drive.example.test/notes.txt'
```

## Limits, compatibility and source map

| Contract | Bound/lifetime |
| --- | --- |
| Browser folder/search pages | 100 default, 200 maximum |
| Photos/Files pages and album membership pages | At most 200; see route-specific defaults |
| Owner cookie / device token / previous token grace | 24 hours / 90 days / at most 15 minutes |
| Browser ordered chunk / native part | At most 16 MiB / fixed 16 MiB except exact final remainder |
| Inactive unfinished upload staging | 24 hours; status polling does not extend it |
| Parts Photos status batch | 100 IDs, 16 KiB body; polling hint 5 seconds |
| Photo hash lookup | 200 queries, 64 KiB body, 20 returned matches/query |
| Source organization batch / lookup | 200 operations or IDs, 2 MiB body |
| Document PUT | 256 MiB, known length and SHA-256 |
| Photo selection / gallery mint | 100,000 selection IDs / 10,000 sealed originals |
| Owner selection / ready owner ZIP | 90 minutes / 90 minutes after preparation |
| Trash / sync journal | 30 days / 8,192 catalog changes |
| Gallery session | 15 minutes; retained payload follows capsule lifecycle |

Host-dependent receive/finalization limits come from capability discovery. A
backlog budget is not a maximum original file size; a larger original can be
admitted alone subject to storage admission. Proxy body/time limits are separate
from server limits. Preview failure or unsupported codecs do not invalidate a
verified stored original.

### Compatibility and intentionally unavailable routes

The production executable constructs the multiuser handler. The legacy
single-owner handler additionally has `POST /api/forge` with `{passphrase,confirm}`;
it is **not** the production account-creation route. Use `/api/bootstrap` on a
fresh multiuser node. Its smaller overlapping API is retained for legacy
integration/tests; new clients should use the multiuser contract here.

The bearer allowlist contains reserved `PATCH /api/v1/photos/collections/{id}`,
`DELETE /api/v1/photos/collections/{id}` and
`PUT /api/v1/backups/uploads/{id}/parts/{index}` patterns without corresponding
operations in the current dispatcher. They are not supported client endpoints.
Use collection action POST and component-qualified parts PUT instead.
`/api/v1/photos/uploads/{id}/receipt` and
`/api/v1/backups/uploads/{id}/receipt` are also unimplemented; use status at the
logical ID. Photos motion/live-maintenance/failure routes currently have no
bearer grant. Enrollment and reauthorization stay owner-cookie-only. Unknown
methods/routes may fall through to a static-file 404 rather than a uniform JSON
405, so do not infer support from an authentication response alone.

### Implementation and maintenance links

| Surface | Source |
| --- | --- |
| Main Desk dispatch and guards | [multi.go](../internal/desk/multi.go), [desk.go](../internal/desk/desk.go), [csrf.go](../internal/desk/csrf.go) |
| Account/profile/admin | [multi_account.go](../internal/desk/multi_account.go), [multi_profile.go](../internal/desk/multi_profile.go), [admin_users.go](../internal/desk/admin_users.go) |
| Library/ordered uploads | [library.go](../internal/desk/library.go), [library_page.go](../internal/desk/library_page.go), [multi_upload.go](../internal/desk/multi_upload.go) |
| Device grants and identity | [device_scopes.go](../internal/users/device_scopes.go), [mobile_identity.go](../internal/desk/mobile_identity.go) |
| Native transports/Files/backups | [mobile_parts.go](../internal/desk/mobile_parts.go), [mobile_files.go](../internal/desk/mobile_files.go), [mobile_backup.go](../internal/desk/mobile_backup.go) |
| Documents | [mobile_documents.go](../internal/desk/mobile_documents.go), [mobile_documents_write.go](../internal/desk/mobile_documents_write.go) |
| Photos and organization | [photo_page.go](../internal/desk/photo_page.go), [mobile_collection.go](../internal/desk/mobile_collection.go), [mobile_source_recovery.go](../internal/desk/mobile_source_recovery.go) |
| Public guests | [share.go](../internal/share/share.go), [gallery.go](../internal/share/gallery.go) |
| WebDAV | [drive.go](../internal/drive/drive.go), [filesystem.go](../internal/drive/filesystem.go) |

When adding a route, update this inventory, its domain contract/OpenAPI (where
applicable), and the exact bearer scope matrix. Authorization-pattern presence
alone does not prove dispatch support. Preserve actual response/status behavior
instead of documenting a proposed uniform API. Operational behavior belongs in
[operations-reference.md](operations-reference.md), [deployment](docker-compose.md)
and [release/rollback](release.md).

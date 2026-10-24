# Library browsing during uploads — October 24, 2026

Each completed upload previously started two independent Library reloads: one
from the upload queue and one from the server event stream. Reloading emptied
the file array, replaced the entire workspace with a loading state, then rendered
the first page again. This detached focused controls and hydrated previews,
discarded loaded pages, and could cancel a folder navigation already in flight.

Upload completion and server notifications now share a 750 ms coalescing timer.
Library background reads leave the displayed snapshot in place, fetch enough
pages to retain the loaded extent, and commit only a complete response for the
current account, folder and query. Navigation, search and load-more requests
take priority. Invalidated pagination leaves the old snapshot visible until a
fresh read succeeds. Events received outside Library/Photos no longer clear
Library data or rebuild the active view.

Library rendering reconciles rows by file ID/folder path and compares their
source markup, preserving unchanged controls, decoded previews and media nodes.
New rows are inserted without replacing the workspace. An existing visible row
anchors the scroll position. Grid revisions invalidate changed previews; media
that remains mounted is not unnecessarily sought back to a saved position.

## Regression verification

`scripts/smoke-library-upload-refresh.py` starts a disposable local node, seeds
a folder spanning two pages, and performs real uploads through all three rails
while real server events refresh the view. It checks row/control identity,
search focus, selection, pagination, viewport anchoring, search updates,
navigation against a delayed stale response, and hydrated grid previews.

```sh
bash scripts/generate-desk-ui.sh
mkdir -p .build
CGO_ENABLED=0 go build -o .build/weazlcloud-upload-ui ./cmd/weazlcloud
# Use a Python interpreter with Playwright installed; requires Chromium.
python3 scripts/smoke-library-upload-refresh.py
WEAZLCLOUD_SMOKE_STORAGE_BACKEND=restic python3 scripts/smoke-library-upload-refresh.py
```

Both backends passed. Overriding `app.js` and `views.js` with the pre-fix source
using `WEAZLCLOUD_SMOKE_UI_DIR` reproduced the regression: the upload refresh
discarded the second page, leaving 100 rows instead of the expected 143.
The candidate image also passed disposable container upload/download,
resumable-finalization/restart and preview checks on both backends, plus the
existing Restic migration check.

`make check` passed, including the complete Go tests, vet, race suite, line
checks and JavaScript checks.

## Production rollout

After the user confirmed the upload batch had finished, both services were
cleanly stopped and a full XFS reflink checkpoint was created at
`/exports/dockervolume/weazlcloud-backups/upload-ui-b39a4cdf397a/data`.
The source and checkpoint inventories matched for all 116,424 regular files
(paths, sizes, owners and permissions); root-level files were also compared
byte-for-byte. Upload staging retained UID/GID 7272 and mode 0700.

Both services now use `weazlcloud:upload-ui-b39a4cdf397a`, built from the previous
runtime image with the tested server binary and embedded UI. The candidate
retains all deployment settings except the image references. Actual container
environment values, mounts, user/security settings and resource limits were
compared after startup. Both services are healthy, the worker probe passes,
and upload staging remains writable as the service user.

The first activation was automatically rolled back because the verifier compared
environment list ordering. The values were identical; normalizing that comparison
allowed the second activation to pass. No data rollback occurred. The verified
pre-update checkpoint and previous image remain available. Private source,
configuration, checkpoint and validation records are under
`/home/bobp/weazlcloud-releases/upload-ui-b39a4cdf397a`.

The live JavaScript hashes match the tested source. Desk, Grab and WebDAV
readiness passed, and Chromium loaded the live login UI without JavaScript
exceptions. Authenticated upload/browsing regression checks used disposable
nodes, not the owner's production account. The frontend is embedded in the
server binary; an already-open browser tab must refresh to load the new UI.

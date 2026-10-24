#!/usr/bin/env python3
"""Real upload/SSE regression on a disposable local node and Chromium.

Build .build/weazlcloud-upload-ui first. Requires Python Playwright and Chromium.
Set WEAZLCLOUD_SMOKE_STORAGE_BACKEND to exercise another storage backend.
"""
import asyncio
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
from urllib.parse import urlparse

from playwright.async_api import async_playwright, expect


async def smoke(base):
    async with async_playwright() as p:
        browser = await p.chromium.launch(executable_path=shutil.which('chromium'), args=['--no-sandbox'])
        context = await browser.new_context(base_url=base, viewport={'width': 1440, 'height': 1000})
        headers = {'X-Weazl-Desk': '1'}
        for _ in range(100):
            try:
                if (await context.request.get('/ready')).ok:
                    break
            except Exception:
                pass
            await asyncio.sleep(.1)
        else:
            raise AssertionError('node did not become ready')
        for path, body in [('/api/bootstrap', {'username': 'upload-test', 'password': 'upload-test-pass', 'vault_passphrase': 'upload-test-pass', 'confirm': 'upload-test-pass'}), ('/api/unlock', {'passphrase': 'upload-test-pass'})]:
            response = await context.request.post(path, data=body, headers=headers)
            assert response.ok, await response.text()

        async def put(path, data=b'fixture'):
            response = await context.request.put('/api/library?path=' + path, data=data, headers=headers)
            assert response.ok, await response.text()

        for i in range(125):
            await put(f'Browse/item-{i:03}.bin')
        await put('Other/seed.bin')
        await put('Previews/keep.txt', b'Keep this hydrated text preview.\n')
        page = await context.new_page()
        if os.environ.get('WEAZLCLOUD_SMOKE_UI_DIR'):
            async def source_override(route):
                source = Path(os.environ['WEAZLCLOUD_SMOKE_UI_DIR']) / Path(urlparse(route.request.url).path).name
                if source.is_file():
                    await route.fulfill(path=str(source), content_type='text/javascript')
                else:
                    await route.continue_()
            await page.route('**/*.js', source_override)
        errors = []
        page.on('pageerror', lambda error: errors.append(str(error)))
        # Keep real transfers in flight long enough to browse through several
        # server events; the server still receives and finalizes every byte.
        async def slow_upload(route):
            if route.request.method == 'PATCH' or route.request.url.endswith('/finalize'):
                await asyncio.sleep(.3)
            await route.continue_()
        await page.route('**/api/uploads/**', slow_upload)
        await page.goto(base + '/#library')
        await page.locator('[data-open-folder="Browse"]').click()
        await expect(page.locator('.tree [data-ctx-file]')).to_have_count(100)
        await page.locator('[data-library-more]').click()
        await expect(page.locator('.tree [data-ctx-file]')).to_have_count(125)
        selected = page.locator('[data-drag-file="Browse/item-100.bin"] [data-select-file]')
        await selected.click(modifiers=['Control'])
        await expect(page.locator('.selection-toolbar strong')).to_have_text('1 selected')
        await page.locator('[data-action="upload"]').click()
        await page.locator('[data-library-search]').focus()
        await page.evaluate('''() => {
            window.kept = {workspace: document.querySelector('.library-workspace'),
              search: document.querySelector('[data-library-search]'),
              row: document.querySelector('[data-drag-file="Browse/item-100.bin"]')};
            window.flashes = 0;
            window.observer = new MutationObserver(() => {
              if (!kept.workspace.isConnected || !kept.search.isConnected || !kept.row.isConnected ||
                  !document.querySelector('.tree [data-ctx-file]')) flashes++;
            });
            observer.observe(document.querySelector('#content'), {childList:true, subtree:true});
        }''')
        await page.locator('#upload').set_input_files([
            {'name': f'zz-upload-{i:02}.bin', 'mimeType': 'application/octet-stream', 'buffer': b'x' * 65536}
            for i in range(18)
        ])
        await expect(page.locator('#upload-tray strong').first).to_have_text('18 of 18 uploaded', timeout=60000)
        await expect(page.locator('.tree [data-ctx-file]')).to_have_count(143, timeout=15000)
        assert await page.evaluate('flashes') == 0, 'background refresh detached the visible Library'
        assert await page.evaluate('document.activeElement === kept.search'), 'search lost focus'
        await expect(page.locator('.selection-toolbar strong')).to_have_text('1 selected')
        await page.evaluate('observer.disconnect()')
        print('PASS: real three-rail uploads + SSE preserve rows, focus, selection and loaded pages', flush=True)

        # Inserting above the viewport must retain the visible row's position.
        await page.locator('[data-drag-file="Browse/item-100.bin"]').scroll_into_view_if_needed()
        await page.evaluate('window.anchorTop = kept.row.getBoundingClientRect().top')
        await put('Browse/aaa-before.bin')
        await expect(page.locator('.tree [data-ctx-file]')).to_have_count(144)
        assert await page.evaluate('Math.abs(kept.row.getBoundingClientRect().top - anchorTop) < 2')

        search = page.locator('[data-library-search]')
        await search.fill('item-0')
        await expect(page.locator('.tree [data-ctx-file]')).to_have_count(100)
        await search.focus()
        await page.evaluate('window.searchNode = document.activeElement')
        await put('Browse/item-0-new.bin')
        await page.wait_for_timeout(1800)
        assert await page.evaluate('document.activeElement === searchNode && searchNode.value === "item-0"')
        await search.fill('')
        await expect(page.locator('.tree [data-ctx-file]')).to_have_count(100)
        print('PASS: viewport anchor and search survive background changes', flush=True)

        # Navigation must win over an in-flight, now stale background response.
        held = asyncio.Event()
        release = asyncio.Event()
        async def hold_page(route):
            response = await route.fetch()
            held.set()
            await release.wait()
            await route.fulfill(response=response)
        await page.route('**/api/library/page?path=Browse&*', hold_page, times=1)
        await put('Browse/zz-navigation.bin')
        await asyncio.wait_for(held.wait(), timeout=10)
        await page.locator('[data-library-path=""]').first.click()
        await page.locator('[data-open-folder="Other"]').click()
        release.set()
        await expect(page.locator('[data-drag-file="Other/seed.bin"]')).to_be_visible()
        await page.wait_for_timeout(1000)
        await expect(page.locator('[data-drag-file^="Browse/"]')).to_have_count(0)
        print('PASS: navigation preempts a stale upload refresh', flush=True)

        await page.locator('[data-library-path=""]').first.click()
        await page.locator('[data-open-folder="Previews"]').click()
        await page.locator('[data-library-view="grid"]').click()
        await expect(page.locator('[data-grid-text-preview]')).to_contain_text('Keep this hydrated', timeout=15000)
        await page.evaluate('window.previewNode = document.querySelector("[data-grid-text-preview]")')
        await put('Previews/new.bin')
        await expect(page.locator('.library-grid [data-ctx-file]')).to_have_count(2)
        assert await page.evaluate('previewNode === document.querySelector("[data-grid-text-preview]") && previewNode.isConnected')
        assert not errors, errors
        print('PASS: hydrated grid previews stay mounted; no browser exceptions', flush=True)
        await browser.close()


with tempfile.TemporaryDirectory(prefix='weazl-upload-ui-') as root:
    port = int(os.environ.get('WEAZLCLOUD_BROWSER_PORT', '29772'))
    env = {**os.environ, 'WEAZLCLOUD_DATA': root + '/data',
           'WEAZLCLOUD_DESK_ADDR': f'127.0.0.1:{port}',
           'WEAZLCLOUD_SHARE_ADDR': f'127.0.0.1:{port+1}',
           'WEAZLCLOUD_DRIVE_ADDR': f'127.0.0.1:{port+2}',
           'WEAZLCLOUD_STORAGE_BACKEND': os.environ.get('WEAZLCLOUD_SMOKE_STORAGE_BACKEND', 'shared-experimental'),
           'PATH': str(Path('.build').resolve()) + os.pathsep + os.environ['PATH']}
    with open(root + '/server.log', 'w+') as log:
        server = subprocess.Popen(['.build/weazlcloud-upload-ui'], env=env, stdout=log, stderr=log)
        try:
            asyncio.run(smoke(f'http://127.0.0.1:{port}'))
        except BaseException:
            log.seek(0)
            print(log.read()[-5000:])
            raise
        finally:
            server.terminate()
            server.wait(timeout=90)

import { readFileSync } from 'node:fs';
import { expect, test, type FrameLocator, type Locator } from '@playwright/test';

const root = '/api/v1/transcription';
const specPath = '/api/v1/docs/spec';
const catalogPath = root + '/models';
const listPath = root + '/list?page=1&limit=10';
const statusPath = root + '/second-demo/status';
const transcriptPath = root + '/second-demo/transcript?read_only=true';
const multipartPath = root + '/upload-multitrack';
const chunkRoute = root + '/uploads/{id}/files/{file_id}/chunks/{index}';
const chunkPath = root + '/uploads/sample-session/files/audio-0/chunks/0';
const referenceSpec = JSON.parse(readFileSync(new URL('../../../api-docs/openapi.json', import.meta.url), 'utf8'));
const exampleResponses: Record<string, unknown> = {
    [listPath]: { jobs: [{ id: 'first-demo', title: 'Library opening' }, { id: 'second-demo', title: 'Planning check-in' }] },
    [statusPath]: { id: 'second-demo', status: 'completed' },
    [transcriptPath]: { job_id: 'second-demo', transcript: { text: 'Our next meeting is on Thursday.' } },
    [catalogPath]: { models: [{ id: 'sample-model' }] },
};

async function editRequest(viewer: FrameLocator, method: string, path: string) {
    const operation = viewer.locator(`.opblock-${method}`).filter({ has: viewer.getByText(path, { exact: true }) });
    await operation.locator('button.opblock-summary-control').click();
    await operation.getByRole('button', { name: 'Try it out' }).click();
    return operation;
}

async function copyRequest(operation: Locator, viewer: FrameLocator): Promise<string> {
    await operation.getByRole('button', { name: 'Build cURL' }).click();
    const snippet = operation.locator('.request-snippets pre');
    await expect(snippet).toBeVisible();
    await expect(operation.getByText('Server response', { exact: true })).toHaveCount(0);
    const body = viewer.locator('body');
    // Observe Swagger UI's real copy event without writing to the test runner's
    // system clipboard. The frame keeps an opaque origin throughout.
    await body.evaluate(element => {
        document.addEventListener('copy', event => {
            element.setAttribute('data-test-copied', window.getSelection()?.toString() ?? '');
            event.preventDefault();
        }, { capture: true, once: true });
    });
    await operation.getByRole('button', { name: 'Copy request snippet to clipboard' }).click();
    const command = await snippet.innerText();
    await expect(body).toHaveAttribute('data-test-copied', command);
    return command;
}

test('keeps the Swagger page and viewer out of the service-worker precache', async ({ request }) => {
    const response = await request.get('/service-worker.js');
    expect(response.ok()).toBe(true);
    expect(await response.text()).not.toMatch(/assets\/(?:SwaggerPage-|swagger-frame-|swagger-ui-)/);
});

test('runs four reviewed reads and copies both upload forms without frame network access', async ({ page, baseURL }) => {
    test.setTimeout(60_000);
    const payload = Buffer.from(JSON.stringify({ exp: 4_000_000_000, sub: 'synthetic-owner' })).toString('base64url');
    const previewToken = `test.${payload}.synthetic`;
    const frameRequests: string[] = [];
    const referenceRequests: string[] = [];
    const exampleRequests: string[] = [];
    const unexpectedAPIRequests: string[] = [];
    const requestPaths: string[] = [];
    const origin = new URL(baseURL!).origin;
    page.on('request', request => {
        const url = new URL(request.url());
        if (request.frame().parentFrame() && (url.origin !== origin || url.pathname.startsWith('/api/'))) {
            frameRequests.push(request.method() + ' ' + url.pathname);
        }
        requestPaths.push(url.pathname);
    });
    await page.route('**/health', route => route.fulfill({ json: { status: 'healthy', version: 'preview' } }));
    await page.route('**/api/**', async route => {
        const request = route.request();
        const url = new URL(request.url());
        const path = url.pathname;
        const fullPath = path + url.search;
        if (path === '/api/v1/auth/registration-status') return route.fulfill({ json: { registration_enabled: false } });
        if (path === '/api/v1/auth/login') return route.fulfill({ json: { token: previewToken, user: { id: 1, username: 'preview' } } });
        if (path === '/api/v1/auth/logout') return route.fulfill({ json: {} });
        if (path === '/api/v1/profiles/' && request.method() === 'GET' && !request.frame().parentFrame()) {
            return route.fulfill({ json: [] });
        }
        if (path === specPath || fullPath in exampleResponses) {
            if (path === specPath) referenceRequests.push(request.method());
            else exampleRequests.push(request.method() + ' ' + fullPath);
            expect(request.headers().authorization).toBe(`Bearer ${previewToken}`);
            return route.fulfill({
                headers: { 'cache-control': 'private, no-store' },
                json: path === specPath ? referenceSpec : exampleResponses[fullPath],
            });
        }
        unexpectedAPIRequests.push(request.method() + ' ' + fullPath);
        return route.fulfill({ status: 404, json: {} });
    });

    await page.goto('/swagger/?url=/unbundled-example.json');
    await expect(page.getByRole('button', { name: 'Sign In', exact: true })).toBeVisible();
    const iframe = page.locator('iframe[title="Swagger API reference"]');
    await expect(iframe).toHaveCount(0);
    expect(referenceRequests).toEqual([]);
    expect(requestPaths.some(path => /\/swagger-ui-bundle-/.test(path))).toBe(false);
    await page.getByLabel('Username', { exact: true }).fill('preview');
    await page.getByLabel('Password', { exact: true }).fill('synthetic-preview-only');
    await page.getByRole('button', { name: 'Sign In', exact: true }).click();

    const viewer = page.frameLocator('iframe[title="Swagger API reference"]');
    await expect(viewer.getByRole('heading', { name: /Jotist API/ })).toBeVisible();
    await expect(iframe).toHaveAttribute('sandbox', 'allow-scripts');
    await expect(viewer.locator('html')).not.toHaveClass(/dark-mode/);
    expect(await iframe.evaluate(element => (element as HTMLIFrameElement).contentDocument)).toBeNull();
    expect(referenceRequests).toEqual(['GET']);
    const renderedSpec = JSON.parse((await viewer.locator('#jotist-api-spec').textContent())!);
    expect(renderedSpec.servers).toEqual([{ url: origin }]);
    await expect(iframe).toHaveAttribute('srcdoc', /connect-src 'none'/);
    expect(await iframe.getAttribute('srcdoc')).not.toContain(previewToken);

    const quickChecks = page.locator('details:has(+ iframe[title="Swagger API reference"])');
    const toggle = quickChecks.locator(':scope > summary');
    const example = quickChecks.getByRole('combobox', { name: 'Example' });
    const run = quickChecks.getByRole('button', { name: 'Run', exact: true });
    await expect(toggle).toHaveText('Quick checks');
    await expect(quickChecks).toHaveJSProperty('open', false);
    await expect(run).toBeHidden();
    expect(exampleRequests).toEqual([]);
    await toggle.press('Enter');
    await expect(quickChecks).toHaveJSProperty('open', true);
    await expect(run).toBeVisible();
    await expect(example.locator('option')).toHaveCount(4);
    await example.selectOption('status');
    await page.getByLabel(/^Recording ID/).fill('invalid recording id');
    await expect(run).toBeDisabled();
    await example.selectOption('list');
    await run.click();
    await expect(page.getByText('HTTP 200', { exact: true })).toBeVisible();
    await page.getByText('Response JSON', { exact: true }).click();
    await expect(page.getByText(/Library opening/)).toBeVisible();

    await example.selectOption('status');
    await page.getByRole('combobox', { name: 'Recording', exact: true }).selectOption('second-demo');
    await run.click();
    await expect(page.getByText('HTTP 200', { exact: true })).toBeVisible();
    await example.selectOption('transcript');
    await run.click();
    await expect(page.getByText('HTTP 200', { exact: true })).toBeVisible();
    await page.getByText('Response JSON', { exact: true }).click();
    await expect(page.getByText(/Our next meeting is on Thursday/)).toBeVisible();
    await example.selectOption('models');
    await run.click();
    await expect(page.getByText('HTTP 200', { exact: true })).toBeVisible();
    await toggle.click();
    await expect(quickChecks).toHaveJSProperty('open', false);
    await expect(run).toBeHidden();

    const catalog = await editRequest(viewer, 'get', catalogPath);
    expect(await copyRequest(catalog, viewer)).toContain(`'${origin}${catalogPath}'`);

    const multipart = await editRequest(viewer, 'post', multipartPath);
    await multipart.getByRole('button', { name: 'Add string item' }).click();
    const selectedFiles = multipart.locator('input[type=file]');
    await expect(selectedFiles).toHaveCount(3);
    await selectedFiles.nth(0).setInputFiles({ name: 'project.aup', mimeType: 'application/xml', buffer: Buffer.from('<project/>') });
    await selectedFiles.nth(1).setInputFiles({ name: 'left mic.wav', mimeType: 'audio/wav', buffer: Buffer.from('left') });
    await selectedFiles.nth(2).setInputFiles({ name: 'right.wav', mimeType: 'audio/wav', buffer: Buffer.from('right') });
    await multipart.getByPlaceholder('title').fill('Library opening');
    const themeToggle = page.getByRole('button', { name: 'Toggle theme' });
    await themeToggle.click();
    await expect(viewer.locator('html')).toHaveClass(/dark-mode/);
    await expect(viewer.locator('#swagger-ui .swagger-ui')).toHaveCSS('background-color', 'rgb(23, 23, 23)');
    await expect(multipart.getByPlaceholder('title')).toHaveValue('Library opening');
    const multipartCommand = await copyRequest(multipart, viewer);
    expect(multipartCommand).toContain(`'${origin}${multipartPath}'`);
    expect(multipartCommand).toContain("-F 'aup=@project.aup;type=application/xml'");
    expect(multipartCommand).toContain("-F 'files=@left mic.wav;type=audio/wav'");
    expect(multipartCommand).toContain("-F 'files=@right.wav;type=audio/wav'");
    expect(multipartCommand).toContain("-F 'title=Library opening'");

    const chunk = await editRequest(viewer, 'put', chunkRoute);
    for (const [name, value] of Object.entries({
        id: 'sample-session', file_id: 'audio-0', index: '0',
        'X-Upload-Token': 'UPLOAD_TOKEN', 'Content-Range': 'bytes 0-5/6', 'X-Chunk-SHA256': 'a'.repeat(64),
    })) {
        await chunk.locator(`[data-param-name="${name}"] input`).fill(value);
    }
    const chunkFile = chunk.locator('input[type=file]');
    await chunkFile.setInputFiles({ name: 'chunk 0.bin', mimeType: 'application/octet-stream', buffer: Buffer.from('sample') });
    const binaryCommand = await copyRequest(chunk, viewer);
    expect(binaryCommand).toContain(`'${origin}${chunkPath}'`);
    expect(binaryCommand).toContain("-H 'Content-Type: application/octet-stream'");
    expect(binaryCommand).toContain("-H 'Content-Range: bytes 0-5/6'");
    expect(binaryCommand).toContain("-H 'X-Upload-Token: UPLOAD_TOKEN'");
    expect(binaryCommand).toContain("--data-binary '@chunk 0.bin'");
    for (const command of [multipartCommand, binaryCommand]) {
        expect(command).toContain("-H 'X-API-Key: YOUR_JOTIST_API_KEY'");
        expect(command).not.toContain(previewToken);
    }
    await chunkFile.setInputFiles({ name: "Saturday's chunk.bin", mimeType: 'application/octet-stream', buffer: Buffer.from('sample') });
    expect(await copyRequest(chunk, viewer)).toContain("--data-binary '@UPLOAD_FILE_1'");
    await themeToggle.click();
    await expect(viewer.locator('html')).not.toHaveClass(/dark-mode/);
    await expect(chunk.locator('.request-snippets pre')).toContainText('@UPLOAD_FILE_1');

    expect(exampleRequests).toEqual([listPath, statusPath, transcriptPath, catalogPath].map(path => 'GET ' + path));
    expect(frameRequests).toEqual([]);
    expect(unexpectedAPIRequests).toEqual([]);
    expect(await iframe.getAttribute('srcdoc')).not.toContain('Our next meeting is on Thursday');
    expect(requestPaths).not.toContain('/unbundled-example.json');
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
        await page.evaluate(() => window.innerWidth),
    );

    await page.getByRole('button', { name: 'Open menu', exact: true }).click();
    await page.getByRole('menuitem', { name: 'Logout', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Sign In', exact: true })).toBeVisible();
    await expect(iframe).toHaveCount(0);
});

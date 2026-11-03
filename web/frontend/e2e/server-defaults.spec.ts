import { expect, test, type Page } from '@playwright/test';
import fixtureData from './fixtures/server-defaults.json' with { type: 'json' };

async function setup(page: Page, device: 'cpu' | 'cuda') {
    const writes: { path: string; body: { parameters: Record<string, unknown> } }[] = [];
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.route('**/health', route => route.fulfill({ json: { status: 'healthy', version: 'preview', commit: 'a'.repeat(40) } }));
    await page.route('**/api/**', async route => {
        const req = route.request();
        const path = new URL(req.url()).pathname.replace(/\/$/, '');
        if (path === '/api/v1/auth/registration-status') return route.fulfill({ json: { registration_enabled: false } });
        if (path === '/api/v1/auth/login') {
            const payload = Buffer.from(JSON.stringify({ exp: 4_000_000_000 })).toString('base64url');
            return route.fulfill({ json: { token: `fixture.${payload}.synthetic`, user: { id: 1, username: 'preview' } } });
        }
        if (path === '/api/v1/llm/config') return route.fulfill({ json: { is_active: false } });
        if (path === '/api/v1/transcription/models') return route.fulfill({ json: { models: fixtureData.models, languages: ['en', 'fr'], execution_defaults: { os: 'linux', architecture: device === 'cpu' ? 'arm64' : 'amd64', default_device: device } } });
        if (path === '/api/v1/user/settings') return route.fulfill({ json: fixtureData.settings });
        if (path === '/api/v1/user/default-profile') return route.fulfill({ status: 404, json: {} });
        if (path === '/api/v1/admin/queue/settings') return route.fulfill({ json: { workers: 1, busy_workers: 0, max_workers: 16, environment_override: false } });
        if (path.startsWith('/api/v1/profiles') && req.method() !== 'GET') {
            writes.push({ path, body: req.postDataJSON() });
            return route.fulfill({ status: req.method() === 'POST' ? 201 : 200, json: { ...fixtureData.profile, ...req.postDataJSON() } });
        }
        if (path === '/api/v1/profiles') return route.fulfill({ json: [fixtureData.profile] });
        throw new Error(`Unexpected fixture endpoint ${req.method()} ${path}`);
    });
    await page.goto('/settings?tab=transcription');
    await page.getByLabel('Username').fill('preview');
    await page.getByLabel('Password', { exact: true }).fill('synthetic-password');
    await page.getByRole('button', { name: 'Sign In', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Create New Profile', exact: true })).toBeEnabled();
    return { writes, errors };
}

test('ARM new profiles use CPU while the browser viewport does not affect the target', async ({ page }) => {
    const { writes, errors } = await setup(page, 'cpu');
    await page.getByRole('button', { name: 'Create New Profile', exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByLabel('Profile Name')).toHaveValue('CPU Whisper small + DiariZen');
    await dialog.getByRole('button', { name: 'Save new profile', exact: true }).click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0].body.parameters).toMatchObject({ model: 'small', device: 'cpu', compute_type: 'float32', fp16: false, diarization_device: 'cpu', diarize_model: 'diarizen', execution_policy_source: 'global' });
    expect(errors).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
});

test('ARM Quick Add offers CPU templates and submits their explicit CPU settings', async ({ page }) => {
    const { writes } = await setup(page, 'cpu');
    await page.getByRole('button', { name: 'Quick Add Presets', exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByText('CPU starting points', { exact: true })).toBeVisible();
    await expect(dialog.getByText('GPU starting points', { exact: true })).toHaveCount(0);
    await dialog.getByRole('checkbox').first().check();
    await dialog.getByRole('button', { name: 'Add selected (1)', exact: true }).click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0].body.parameters).toMatchObject({ device: 'cpu', compute_type: 'float32', diarization_device: 'cpu' });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
});

test('opening and saving an existing GPU profile on ARM preserves its explicit configuration', async ({ page }) => {
    const { writes } = await setup(page, 'cpu');
    await page.getByText('Synthetic profile for preservation checks', { exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Save Profile', exact: true }).click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0].path).toBe('/api/v1/profiles/saved-gpu');
    expect(writes[0].body.parameters).toMatchObject(fixtureData.profile.parameters);
});

test('AMD64 keeps its GPU-first new profile and existing Quick Add templates', async ({ page }) => {
    const { writes } = await setup(page, 'cuda');
    await page.getByRole('button', { name: 'Quick Add Presets', exact: true }).click();
    await expect(page.getByRole('dialog').getByText('GPU starting points', { exact: true })).toBeVisible();
    await page.getByRole('dialog').getByText('CPU and hybrid alternatives', { exact: true }).click();
    await expect(page.getByRole('dialog').getByRole('checkbox')).toHaveCount(12);
    await page.keyboard.press('Escape');
    await page.getByRole('button', { name: 'Create New Profile', exact: true }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByLabel('Profile Name')).toHaveValue('GPU Qwen3 ASR 1.7B + Pyannote');
    await dialog.getByRole('button', { name: 'Save new profile', exact: true }).click();
    await expect.poll(() => writes.length).toBe(1);
    expect(writes[0].body.parameters).toMatchObject({ device: 'cuda', compute_type: 'float16', diarization_device: 'same' });
});

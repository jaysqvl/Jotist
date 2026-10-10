import { expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import type { CachedModel, ModelInventory } from '../src/features/settings/components/modelStorage';

// UI fixtures are synthetic. Real authentication and persistent writes are
// exercised separately by scripts/smoke_container.py against the built image.
const models: CachedModel[] = [
    { id: 'cohere', name: 'Cohere Transcribe 2B', source: 'CohereLabs/cohere-transcribe-03-2026', purpose: 'Recognition', bytes: 2_500_000_000, removable_bytes: 2_500_000_000, shared_bytes: 0, revisions: 1, locations: 1, status: 'queued', can_remove: true, version: 'cohere-v1', profiles: 1, cached_only_profiles: 0, uses: [{ state: 'queued', recording_title: 'Project planning', stage: 'Recognition' }] },
    { id: 'parakeet', name: 'NVIDIA Parakeet', source: 'nvidia/parakeet-tdt-0.6b-v3', purpose: 'Recognition', bytes: 1_200_000_000, removable_bytes: 1_200_000_000, shared_bytes: 0, revisions: 1, locations: 1, status: 'in_use', can_remove: false, reason: 'An active run is using these files.', version: 'parakeet-v1', profiles: 1, cached_only_profiles: 0, uses: [{ state: 'in_use', recording_title: 'Short interview', stage: 'Recognition' }] },
];

async function setup(page: Page, rejectRemoval = false) {
    let entries = structuredClone(models);
    const unexpected: string[] = [];
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.route('**/health', route => route.fulfill({ json: { status: 'healthy', version: 'preview', commit: '0123456789012345678901234567890123456789' } }));
    await page.route('**/api/**', async route => {
        const request = route.request();
        const url = new URL(request.url());
        if (url.pathname === '/api/v1/auth/registration-status') return route.fulfill({ json: { registration_enabled: false } });
        if (url.pathname === '/api/v1/auth/login') {
            const payload = Buffer.from(JSON.stringify({ exp: 4_000_000_000, sub: 'synthetic-owner' })).toString('base64url');
            return route.fulfill({ json: { token: `test.${payload}.synthetic`, user: { id: 1, username: 'preview' } } });
        }
        if (url.pathname === '/api/v1/llm/config') return route.fulfill({ json: { is_active: false } });
        if (url.pathname === '/api/v1/profiles/') return route.fulfill({ json: [] });
        if (url.pathname === '/api/v1/settings/models/events') return route.fulfill({ status: 403, json: { error: 'Synthetic fixture has no event stream' } });
        if (url.pathname === '/api/v1/settings/models') {
            expect(request.headers().authorization).toContain('Bearer test.');
            const inventory: ModelInventory = { scanned_at: '2026-10-09T12:00:00Z', model_bytes: entries.reduce((sum, model) => sum + model.bytes, 0), runtime_bytes: 500_000_000, package_cache_bytes: 200_000_000, volumes: [{ total_bytes: 100_000_000_000, available_bytes: 50_000_000_000 }], entries, warnings: [] };
            return route.fulfill({ json: inventory });
        }
        if (url.pathname === '/api/v1/settings/models/cohere' && request.method() === 'DELETE') {
            expect(request.postDataJSON()).toEqual({ version: 'cohere-v1' });
            if (rejectRemoval) return route.fulfill({ status: 409, json: { error: 'An active run is now using these files. Refresh and try again when it finishes.' } });
            entries = entries.filter(model => model.id !== 'cohere');
            return route.fulfill({ json: { message: 'Model files removed. Saved profiles and runs are unchanged.' } });
        }
        unexpected.push(`${request.method()} ${url.pathname}`);
        return route.fulfill({ status: 501, json: { error: 'Unexpected fixture request' } });
    });
    await page.goto('/settings?tab=models');
    await page.getByLabel('Username', { exact: true }).fill('preview');
    await page.getByLabel('Password', { exact: true }).fill('synthetic-preview-only');
    await page.getByRole('button', { name: 'Sign In', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Model storage', exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Cohere Transcribe 2B', exact: true })).toBeVisible();
    return { unexpected, errors };
}

async function noOverflow(page: Page) {
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)).toBe(true);
}

test('model storage fits every viewport and supports accessible filtering', async ({ page }, testInfo) => {
    const state = await setup(page);
    await noOverflow(page);
    await expect(page.getByRole('button', { name: 'Remove NVIDIA Parakeet', exact: true })).toBeDisabled();
    await page.getByLabel('Search downloaded models').fill('Cohere');
    await expect(page.getByRole('heading', { name: 'NVIDIA Parakeet', exact: true })).toHaveCount(0);
    await page.getByLabel('Search downloaded models').fill('');
    await page.getByLabel('Filter downloaded models').selectOption('busy');
    await expect(page.getByRole('heading', { name: 'Cohere Transcribe 2B', exact: true })).toHaveCount(0);
    await page.getByLabel('Filter downloaded models').selectOption('all');
    await page.getByRole('button', { name: 'Details', exact: true }).first().click();
    await expect(page.getByText('Source: CohereLabs/cohere-transcribe-03-2026', { exact: true })).toBeVisible();
    await noOverflow(page);
    const accessibility = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
    expect(accessibility.violations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('model-storage.png'), fullPage: true });
    expect(state.unexpected).toEqual([]);
    expect(state.errors).toEqual([]);
});

test('queued-model removal confirms download behavior and preserves active files', async ({ page }, testInfo) => {
    const state = await setup(page);
    await page.getByRole('button', { name: 'Remove Cohere Transcribe 2B', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Remove Cohere Transcribe 2B?' });
    await expect(dialog.getByText('Queued runs may need these files. They will download missing files before processing.')).toBeVisible();
    await noOverflow(page);
    expect((await new AxeBuilder({ page }).include('[role="dialog"]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()).violations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('model-removal.png'), fullPage: true });
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Remove Cohere Transcribe 2B', exact: true })).toBeFocused();
    await page.getByRole('button', { name: 'Remove Cohere Transcribe 2B', exact: true }).click();
    await page.getByRole('button', { name: 'Remove model files', exact: true }).click();
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(page.getByRole('heading', { name: 'Cohere Transcribe 2B', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Remove NVIDIA Parakeet', exact: true })).toBeDisabled();
    await expect(page.getByLabel('Search downloaded models')).toBeFocused();
    expect(state.unexpected).toEqual([]);
    expect(state.errors).toEqual([]);
});

test('a removal conflict remains visible without hiding files', async ({ page }) => {
    const state = await setup(page, true);
    await page.getByRole('button', { name: 'Remove Cohere Transcribe 2B', exact: true }).click();
    await page.getByRole('button', { name: 'Remove model files', exact: true }).click();
    await expect(page.getByRole('alert')).toContainText('An active run is now using these files.');
    await expect(page.getByRole('dialog')).toBeVisible();
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Cohere Transcribe 2B', exact: true })).toBeVisible();
    expect(state.unexpected).toEqual([]);
    expect(state.errors).toEqual([]);
});

test('model storage and removal remain readable in dark mode', async ({ page }, testInfo) => {
    const state = await setup(page);
    await page.getByRole('button', { name: 'Toggle theme', exact: true }).click();
    await expect(page.locator('html')).toHaveClass(/dark/);
    await noOverflow(page);
    expect((await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze()).violations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('model-storage-dark.png'), fullPage: true });
    await page.getByRole('button', { name: 'Remove Cohere Transcribe 2B', exact: true }).click();
    await expect(page.getByRole('dialog')).toBeVisible();
    expect((await new AxeBuilder({ page }).include('[role="dialog"]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()).violations).toEqual([]);
    expect(state.unexpected).toEqual([]);
    expect(state.errors).toEqual([]);
});

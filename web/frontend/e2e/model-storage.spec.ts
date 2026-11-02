import { expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import type { CachedModel, ModelInventory, ModelRetentionPolicy } from '../src/features/settings/components/modelStorage';

// UI fixtures are synthetic. Real authentication and persistent writes are
// exercised separately by scripts/smoke_container.py against the built image.
const models: CachedModel[] = [
    { id: 'cohere', name: 'Cohere Transcribe 2B', source: 'CohereLabs/cohere-transcribe-03-2026', purpose: 'Recognition', bytes: 2_500_000_000, removable_bytes: 2_500_000_000, shared_bytes: 0, revisions: 1, locations: 1, status: 'queued', can_remove: true, version: 'cohere-v1', profiles: 1, cached_only_profiles: 0, uses: [{ state: 'queued', recording_title: 'Project planning', stage: 'Recognition' }] },
    { id: 'parakeet', name: 'NVIDIA Parakeet', source: 'nvidia/parakeet-tdt-0.6b-v3', purpose: 'Recognition', bytes: 1_200_000_000, removable_bytes: 1_200_000_000, shared_bytes: 0, revisions: 1, locations: 1, status: 'in_use', can_remove: false, reason: 'An active run is using these files.', version: 'parakeet-v1', profiles: 1, cached_only_profiles: 0, uses: [{ state: 'in_use', recording_title: 'Short interview', stage: 'Recognition' }] },
    { id: 'unregistered', name: 'Other cached repository', source: 'example/unregistered', purpose: 'Other cached repository', bytes: 500_000_000, removable_bytes: 500_000_000, shared_bytes: 0, revisions: 1, locations: 1, status: 'downloaded', can_remove: false, reason: 'This repository is not managed by a registered Jotist model.', version: 'unregistered-v1', profiles: 0, cached_only_profiles: 0, uses: [] },
];

async function setup(page: Page, rejectRemoval = false, rejectCleanup = false) {
    let entries = structuredClone(models);
    let retention: ModelRetentionPolicy = { enabled: true, retention_days: 14, last_checked_at: '2026-11-02T12:00:00Z', last_removed_models: 0, last_removed_bytes: 0, last_skipped_models: 0, last_failed_models: 0 };
    const cleanupVersion = 'a'.repeat(64);
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
        if (url.pathname === '/api/v1/settings/models/retention' && request.method() === 'PUT') {
            const value = request.postDataJSON();
            expect(typeof value.enabled).toBe('boolean');
            expect(Number.isInteger(value.retention_days)).toBe(true);
            expect(value.retention_days).toBeGreaterThanOrEqual(1);
            expect(value.retention_days).toBeLessThanOrEqual(365);
            retention = { ...retention, ...value };
            return route.fulfill({ json: retention });
        }
        if (url.pathname === '/api/v1/settings/models/cleanup') {
            if (request.method() === 'GET') return route.fulfill({ json: { mode: 'all', version: cleanupVersion, models: entries.filter(model => model.can_remove).map(model => ({ id: model.id, name: model.name, version: model.version, bytes: model.removable_bytes })), removable_bytes: entries.filter(model => model.can_remove).reduce((sum, model) => sum + model.removable_bytes, 0), skipped_models: entries.filter(model => !model.can_remove).length, cached_only_profiles: false } });
            if (request.method() === 'POST') {
                expect(request.postDataJSON()).toEqual({ mode: 'all', version: cleanupVersion });
                if (rejectCleanup) return route.fulfill({ status: 409, json: { error: 'The cache changed. Close this dialog and review a fresh preview.' } });
                const removed = entries.filter(model => model.can_remove);
                entries = entries.filter(model => !model.can_remove);
                return route.fulfill({ json: { checked_at: '2026-11-02T12:00:00Z', removed_models: removed.length, removed_bytes: removed.reduce((sum, model) => sum + model.removable_bytes, 0), skipped_models: entries.length, failed_models: 0, message: `Removed ${removed.length} cached models. Profiles and completed runs are unchanged. ${entries.length} models were kept.` } });
            }
        }
        if (url.pathname === '/api/v1/settings/models') {
            expect(request.headers().authorization).toContain('Bearer test.');
            const inventory: ModelInventory = { scanned_at: '2026-11-02T12:00:00Z', model_bytes: entries.reduce((sum, model) => sum + model.bytes, 0), runtime_bytes: 500_000_000, package_cache_bytes: 200_000_000, volumes: [{ total_bytes: 100_000_000_000, available_bytes: 50_000_000_000 }], entries, warnings: [], retention };
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

test('automatic cleanup defaults to fourteen days and remembers saved settings', async ({ page }) => {
    const state = await setup(page);
    await expect(page.getByRole('switch', { name: 'Automatic model cleanup' })).toBeChecked();
    await expect(page.getByLabel('Model retention days', { exact: true })).toHaveValue('14');
    await page.getByRole('switch', { name: 'Automatic model cleanup' }).click();
    await page.getByLabel('Model retention days', { exact: true }).fill('21');
    await page.getByRole('button', { name: 'Save changes', exact: true }).click();
    await expect(page.getByText('Saved', { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByRole('switch', { name: 'Automatic model cleanup' })).not.toBeChecked();
    await expect(page.getByLabel('Model retention days', { exact: true })).toHaveValue('21');
    await page.getByLabel('Model retention days', { exact: true }).fill('0');
    await expect(page.getByRole('button', { name: 'Save changes', exact: true })).toBeDisabled();
    await expect(page.getByRole('alert')).toContainText('Choose a whole number from 1 to 365 days.');
    expect(state.unexpected).toEqual([]);
    expect(state.errors).toEqual([]);
});

test('clear cache previews protected models and preserves them after confirmation', async ({ page }, testInfo) => {
    const state = await setup(page);
    const trigger = page.getByRole('button', { name: 'Clear downloaded models', exact: true });
    await trigger.click();
    const dialog = page.getByRole('dialog', { name: 'Clear downloaded models?' });
    await expect(dialog.getByText('2 protected models will be kept.', { exact: true })).toBeVisible();
    await noOverflow(page);
    expect((await new AxeBuilder({ page }).include('[role="dialog"]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()).violations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('clear-model-cache.png') });
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(trigger).toBeFocused();
    await expect(page.getByRole('heading', { name: 'Cohere Transcribe 2B', exact: true })).toBeVisible();
    await trigger.click();
    await page.getByRole('button', { name: 'Clear model files', exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole('heading', { name: 'Cohere Transcribe 2B', exact: true })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Remove NVIDIA Parakeet', exact: true })).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Remove Other cached repository', exact: true })).toBeDisabled();
    expect(state.unexpected).toEqual([]);
    expect(state.errors).toEqual([]);
});

test('an uncertain clear-cache result keeps the confirmation and visible model data', async ({ page }) => {
    const state = await setup(page, false, true);
    await page.getByRole('button', { name: 'Clear downloaded models', exact: true }).click();
    await page.getByRole('button', { name: 'Clear model files', exact: true }).click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await expect(page.getByRole('alert')).toContainText('The cache changed.');
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Cohere Transcribe 2B', exact: true })).toBeVisible();
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
    await page.screenshot({ path: testInfo.outputPath('model-removal.png') });
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
    await expect(page.getByRole('dialog')).toHaveCSS('animation-name', 'none');
    await expect(page.locator('[data-slot="dialog-overlay"]')).toHaveCSS('animation-name', 'none');
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await expect(page.getByRole('dialog')).toHaveCSS('animation-name', 'enter');
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect(page.getByRole('dialog')).toHaveCSS('animation-name', 'none');
    expect((await new AxeBuilder({ page }).include('[role="dialog"]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()).violations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('model-removal-dark.png') });
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();
    await page.getByRole('button', { name: 'Clear downloaded models', exact: true }).click();
    await noOverflow(page);
    expect((await new AxeBuilder({ page }).include('[role="dialog"]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze()).violations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('clear-model-cache-dark.png') });
    expect(state.unexpected).toEqual([]);
    expect(state.errors).toEqual([]);
});

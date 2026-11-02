import { expect, test, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import type { ModelDownload } from '../src/features/settings/components/modelDownloads';

async function fixture(page: Page) {
    let downloads: ModelDownload[] = [];
    const admissions: unknown[] = [];
    const errors: string[] = [];
    page.on('pageerror', e => errors.push(e.message));
    const catalog = [
        { id: 'cohere', source: 'CohereLabs/cohere-transcribe-03-2026', name: 'Cohere Transcribe 2B', purpose: 'Recognition', dependencies: [], gated: false },
        { id: 'diarizen', source: 'BUT-FIT/diarizen-wavlm-large-s80-md-v2', name: 'DiariZen Large s80 v2', purpose: 'Speakers', dependencies: ['pyannote/wespeaker-voxceleb-resnet34-LM'], gated: false },
        { id: 'wespeaker', source: 'pyannote/wespeaker-voxceleb-resnet34-LM', name: 'WeSpeaker embedding', purpose: 'Speaker dependency', dependencies: [], gated: true },
    ];
    await page.route('**/health', route => route.fulfill({ json: { status: 'healthy', version: 'preview', commit: '0123456789012345678901234567890123456789' } }));
    await page.route('**/api/**', async route => {
        const request = route.request();
        const path = new URL(request.url()).pathname;
        if (path === '/api/v1/auth/registration-status') return route.fulfill({ json: { registration_enabled: false } });
        if (path === '/api/v1/auth/login') {
            const payload = Buffer.from(JSON.stringify({ exp: 4_000_000_000 })).toString('base64url');
            return route.fulfill({ json: { token: `test.${payload}.synthetic`, user: { id: 1, username: 'preview' } } });
        }
        if (path === '/api/v1/llm/config') return route.fulfill({ json: { is_active: false } });
        if (path === '/api/v1/profiles/') return route.fulfill({ json: [] });
        if (path === '/api/v1/settings/models/events') return route.fulfill({ status: 403, json: { error: 'No persistent fixture stream' } });
        if (path === '/api/v1/settings/models/catalog') return route.fulfill({ json: { models: catalog } });
        if (path === '/api/v1/settings/models/downloads') {
            if (request.method() === 'GET') return route.fulfill({ json: { downloads, available: true } });
            const body = request.postDataJSON(); admissions.push(body);
            const model = catalog.find(model => model.id === body.model_id)!;
            const existing = downloads.find(job => job.model_id === model.id && ['queued', 'downloading'].includes(job.state));
            const job: ModelDownload = existing ?? { id: `job-${downloads.length}`, model_id: model.id, source: model.source, name: model.name, state: 'downloading', phase: 'downloading', can_cancel: true, keep_downloaded: body.keep_downloaded, downloaded_bytes: 40_000_000, total_bytes: 100_000_000, completed_files: 1, total_files: 4, created_at: '2026-11-02T12:00:00Z', updated_at: '2026-11-02T12:00:01Z' };
            if (!existing) downloads = [job, ...downloads];
            return route.fulfill({ status: 202, json: job });
        }
        if (/\/downloads\/[^/]+\/cancel$/.test(path)) {
            const job = downloads.find(item => item.id === path.split('/').at(-2))!;
            Object.assign(job, { state: 'canceled', phase: 'canceled', can_cancel: false, updated_at: '2026-11-02T12:00:02Z', message: 'Download canceled. Completed files can be reused.' });
            return route.fulfill({ json: job });
        }
        if (path === '/api/v1/settings/models') return route.fulfill({ json: { scanned_at: '2026-11-02T12:00:00Z', model_bytes: 0, runtime_bytes: 0, package_cache_bytes: 0, volumes: [], entries: [], warnings: [], retention: { enabled: true, retention_days: 14, last_removed_models: 0, last_removed_bytes: 0, last_skipped_models: 0, last_failed_models: 0 } } });
        throw new Error(`Unexpected fixture API ${request.method()} ${path}`);
    });
    await page.goto('/settings?tab=models');
    await page.getByLabel('Username').fill('preview');
    await page.getByLabel('Password', { exact: true }).fill('fixture-password');
    await page.getByRole('button', { name: 'Sign In', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Model storage', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Download models', exact: true }).click();
    return { admissions, errors };
}

test('download picker filters models, keeps dependencies and continues outside the dialog', async ({ page }) => {
    const { admissions, errors } = await fixture(page);
    const dialog = page.getByRole('dialog');
    await expect(dialog.getByRole('heading', { name: 'Cohere Transcribe 2B' })).toBeVisible();
    await dialog.getByLabel('Filter models to download').selectOption('speakers');
    await expect(dialog.getByRole('heading', { name: 'Cohere Transcribe 2B' })).toHaveCount(0);
    await expect(dialog.getByText('Includes WeSpeaker embedding')).toBeVisible();
    await dialog.getByRole('switch', { name: 'Keep DiariZen Large s80 v2 after download' }).check();
    await dialog.getByRole('button', { name: 'Download DiariZen Large s80 v2', exact: true }).click();
    expect(admissions).toEqual([{ model_id: 'diarizen', keep_downloaded: true }]);
    await expect(dialog.getByRole('button', { name: 'Download DiariZen Large s80 v2', exact: true })).toBeDisabled();
    await page.keyboard.press('Escape');
    const activity = page.getByRole('region', { name: /Downloads/ });
    await expect(activity.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40');
    await expect(activity.getByText('1/4 files', { exact: false })).toBeVisible();
    await activity.getByRole('button', { name: 'Cancel DiariZen Large s80 v2 download' }).click();
    await expect(activity.getByText('Canceled', { exact: true })).toBeVisible();
    await expect(activity.getByRole('progressbar')).toHaveCount(0);
    expect(errors).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
});

test('download picker is scrollable, keyboard accessible and fits mobile', async ({ page }) => {
    const { errors } = await fixture(page);
    const dialog = page.getByRole('dialog');
    await dialog.getByRole('textbox', { name: 'Search models to download' }).fill('cohere');
    await expect(dialog.getByRole('heading', { name: 'Cohere Transcribe 2B' })).toBeVisible();
    await expect(dialog.getByRole('heading', { name: 'DiariZen Large s80 v2' })).toHaveCount(0);
    const bounds = await dialog.boundingBox();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
    const result = await new AxeBuilder({ page }).include('[role="dialog"]').withTags(['wcag2a', 'wcag2aa', 'wcag21aa']).analyze();
    expect(result.violations).toEqual([]);
    expect(errors).toEqual([]);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)).toBe(true);
});

import test from 'node:test';
import assert from 'node:assert/strict';
import { downloadEvent, downloadProgress, updateDownloadList } from './modelDownloads.ts';
import type { ModelDownload } from './modelDownloads.ts';

const job: ModelDownload = { id: 'one', model_id: 'tiny', source: 'publisher/model', name: 'Model', state: 'downloading', phase: 'downloading', can_cancel: true, keep_downloaded: false, downloaded_bytes: 10, total_bytes: 100, completed_files: 0, total_files: 1, created_at: '2026-11-02T20:00:00Z', updated_at: '2026-11-02T20:00:01Z' };
test('late HTTP/progress responses cannot resurrect a finished download', () => {
    const complete = { ...job, state: 'completed' as const, updated_at: '2026-11-02T20:00:02Z' };
    assert.deepEqual(updateDownloadList({ downloads: [complete] }, job), { downloads: [complete] });
    assert.equal(updateDownloadList({ downloads: [job] }, complete).downloads[0].state, 'completed');
    assert.equal(updateDownloadList({ downloads: [job] }, { ...job, id: 'retry' }).downloads.length, 2);
});
test('unknown progress stays indeterminate and 100 percent means completed', () => {
    assert.equal(downloadProgress({ ...job, total_bytes: 0 }), null);
    assert.equal(downloadProgress({ ...job, downloaded_bytes: 100 }), 99);
    assert.equal(downloadProgress({ ...job, state: 'completed' }), 100);
});
test('stream accepts only bounded typed download progress', () => {
    assert.equal(downloadEvent({ type: 'model_download_changed', job: { ...job, downloaded_bytes: -1 } }), null);
    assert.equal(downloadEvent({ type: 'model_download_changed', job: { ...job, state: 'arbitrary' } }), null);
    assert.equal(downloadEvent({ type: 'other' }), null);
    assert.deepEqual(downloadEvent({ type: 'model_downloads_changed' }), { type: 'model_downloads_changed' });
    assert.deepEqual(downloadEvent({ type: 'model_download_changed', job }), { type: 'model_download_changed', job });
});

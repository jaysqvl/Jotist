import assert from 'node:assert/strict';
import test from 'node:test';
import { modelStatus, modelStorageResponse, queuedRemovalWarning, storageSize, visibleModels } from './modelStorage.ts';
import type { CachedModel } from './modelStorage.ts';

const model = (name: string, bytes: number, overrides: Partial<CachedModel> = {}): CachedModel => ({
    id: name, name, source: `org/${name}`, purpose: 'Recognition', bytes, removable_bytes: bytes,
    shared_bytes: 0, revisions: 1, locations: 1, status: 'downloaded', can_remove: true,
    version: 'version', profiles: 1, cached_only_profiles: 0, uses: [], ...overrides,
});

test('busy and audit-only models remain visible but are excluded from removal filter', () => {
    const models = [model('small', 5), model('active', 10, { can_remove: false, uses: [{ state: 'in_use' }], status: 'in_use' }), model('unmanaged', 50, { can_remove: false })];
    assert.deepEqual(visibleModels(models, '', 'available', 'size').map(model => model.name), ['small']);
    assert.deepEqual(visibleModels(models, '', 'busy', 'name').map(model => model.name), ['active']);
    assert.deepEqual(visibleModels(models, '', 'all', 'size').map(model => model.name), ['unmanaged', 'active', 'small']);
    assert.deepEqual(models.map(model => model.name), ['small', 'active', 'unmanaged']);
});

test('search covers checkpoint source and stage purpose', () => {
    const models = [model('Qwen', 5, { purpose: 'Word timing', source: 'Qwen/ForcedAligner' }), model('Parakeet', 10)];
    assert.equal(visibleModels(models, ' forcedaligner ', 'all', 'name')[0].name, 'Qwen');
    assert.equal(visibleModels(models, 'word timing', 'all', 'size').length, 1);
    assert.equal(visibleModels(models, 'missing', 'all', 'size').length, 0);
});

test('last-use sort places unknown use last and orders actual run timestamps', () => {
    const models = [model('never', 1), model('old', 1, { last_used: '2026-10-01T00:00:00Z' }), model('new', 1, { last_used: '2026-10-02T00:00:00Z' })];
    assert.deepEqual(visibleModels(models, '', 'all', 'last_used').map(model => model.name), ['new', 'old', 'never']);
});

test('queued, active, downloading and partial files have distinct labels', () => {
    assert.equal(modelStatus(model('partial', 1, { status: 'partial' })), 'Partial download');
    assert.equal(modelStatus(model('retained', 1, { status: 'removal_pending' })), 'Cleanup needed');
    assert.equal(modelStatus(model('waiting', 1, { status: 'queued' })), 'Queued');
    assert.equal(modelStatus(model('active', 1, { status: 'in_use', can_remove: false })), 'In use');
    assert.equal(modelStatus(model('download', 1, { status: 'downloading', can_remove: false })), 'Downloading');
    assert.equal(storageSize(0), '0 B');
    assert.equal(storageSize(1024 ** 3 * 1.5), '1.5 GiB');
});

test('queued models can be removed and are excluded from the active filter', () => {
    const models = [model('queued', 10, { status: 'queued', uses: [{ state: 'queued' }] }), model('active', 5, { status: 'in_use', can_remove: false, uses: [{ state: 'in_use' }, { state: 'queued' }] })];
    assert.deepEqual(visibleModels(models, '', 'available', 'name').map(model => model.name), ['queued']);
    assert.deepEqual(visibleModels(models, '', 'busy', 'name').map(model => model.name), ['active']);
});

test('queued removal warns about redownload or immutable cached-only requests', () => {
    assert.equal(queuedRemovalWarning(undefined), null);
    assert.equal(queuedRemovalWarning(model('idle', 1)), null);
    assert.equal(queuedRemovalWarning(model('active', 1, { uses: [{ state: 'in_use' }] })), null);
    assert.match(queuedRemovalWarning(model('queued', 1, { uses: [{ state: 'queued' }] }))!, /download missing files/);
    assert.match(queuedRemovalWarning(model('cached', 1, { uses: [{ state: 'queued' }, { state: 'queued', cached_only: true }] }))!, /could fail after removal/);
    assert.match(queuedRemovalWarning(model('cached', 1, { uses: [{ state: 'queued', cached_only: true }] }))!, /settings stay unchanged/);
});

test('conflict and unavailable responses cannot be mistaken for removal success', async () => {
    await assert.rejects(modelStorageResponse(new Response(JSON.stringify({ error: 'Model is now in use.' }), { status: 409 })), /now in use/);
    await assert.rejects(modelStorageResponse(new Response('proxy unavailable', { status: 502 })), /Refresh to check/);
    const success = await modelStorageResponse<{ message: string }>(new Response(JSON.stringify({ message: 'Removed.' })));
    assert.equal(success.message, 'Removed.');
});

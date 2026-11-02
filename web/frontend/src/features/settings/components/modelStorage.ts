export interface ModelUse {
    recording_id?: string;
    recording_title?: string;
    stage?: string;
    state: string;
    cached_only?: boolean;
}

export interface CachedModel {
    id: string;
    name: string;
    source: string;
    purpose: string;
    bytes: number;
    removable_bytes: number;
    shared_bytes: number;
    revisions: number;
    locations: number;
    status: string;
    can_remove: boolean;
    reason?: string;
    version: string;
    last_used?: string;
    cached_since?: string;
    evict_after?: string;
    retention_reason?: string;
    profiles: number;
    cached_only_profiles: number;
    uses: ModelUse[];
}

export interface ModelInventory {
    scanned_at: string;
    model_bytes: number;
    runtime_bytes: number;
    package_cache_bytes: number;
    volumes: { total_bytes: number; available_bytes: number }[];
    entries: CachedModel[];
    warnings: string[];
    retention?: ModelRetentionPolicy;
}

export interface ModelRetentionPolicy {
    enabled: boolean;
    retention_days: number;
    last_checked_at?: string;
    next_check_at?: string;
    last_removed_models: number;
    last_removed_bytes: number;
    last_skipped_models: number;
    last_failed_models: number;
}

export interface ModelCleanupPlan {
    mode: 'all' | 'expired';
    version: string;
    models: { id: string; name: string; version: string; bytes: number }[];
    removable_bytes: number;
    skipped_models: number;
    cached_only_profiles: boolean;
}

export interface ModelCleanupResult {
    checked_at: string;
    removed_models: number;
    removed_bytes: number;
    skipped_models: number;
    failed_models: number;
    message: string;
}

export type ModelSort = 'size' | 'name' | 'last_used';
export type ModelFilter = 'all' | 'available' | 'busy';

export function visibleModels(models: CachedModel[], search: string, filter: ModelFilter, sort: ModelSort) {
    const needle = search.trim().toLocaleLowerCase();
    return models.filter(model =>
        (!needle || `${model.name} ${model.source} ${model.purpose}`.toLocaleLowerCase().includes(needle)) &&
        (filter === 'all' || (filter === 'available' ? model.can_remove : model.uses.some(use => use.state !== 'queued'))))
        .sort((a, b) => {
            if (sort === 'size') return b.bytes - a.bytes || a.name.localeCompare(b.name);
            if (sort === 'last_used') return Date.parse(b.last_used ?? '') - Date.parse(a.last_used ?? '') ||
                (b.last_used ? 1 : 0) - (a.last_used ? 1 : 0) || a.name.localeCompare(b.name);
            return a.name.localeCompare(b.name);
        });
}

export function storageSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    const unit = Math.min(4, Math.floor(Math.log(bytes) / Math.log(1024)));
    return `${(bytes / 1024 ** unit).toFixed(1)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][unit]}`;
}

export function modelStatus(model: CachedModel): string {
    if (model.status === 'in_use') return 'In use';
    if (model.status === 'queued') return 'Queued';
    if (model.status === 'downloading') return 'Downloading';
    if (model.status === 'removal_pending') return 'Cleanup needed';
    if (!model.can_remove && model.status !== 'partial') return 'Audit only';
    return model.status === 'partial' ? 'Partial download' : 'Downloaded';
}

export function queuedRemovalWarning(model?: CachedModel): string | null {
    const queued = model?.uses.filter(use => use.state === 'queued') ?? [];
    if (!queued.length) return null;
    if (queued.some(use => use.cached_only)) return 'A queued run uses cached files only and could fail after removal. Its queued settings stay unchanged.';
    return 'Queued runs may need these files. They will download missing files before processing.';
}

// Keep HTTP errors visible, including an uncertain deletion response. Never
// remove a row optimistically or interpret a failed response as success.
export async function modelStorageResponse<T>(response: Response): Promise<T> {
    let body: { error?: string } & T;
    try { body = await response.json(); }
    catch { throw new Error('Could not read the server response. Refresh to check model storage.'); }
    if (!response.ok) throw new Error(body.error ?? 'The storage request failed. Refresh to check its current state.');
    return body;
}

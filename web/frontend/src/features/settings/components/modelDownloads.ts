export interface DownloadableModel {
    id: string;
    source: string;
    name: string;
    purpose: string;
    dependencies: string[];
    gated: boolean;
}

export interface ModelDownload {
    id: string;
    model_id: string;
    source: string;
    name: string;
    state: 'queued' | 'downloading' | 'canceling' | 'completed' | 'failed' | 'canceled';
    phase: string;
    can_cancel: boolean;
    keep_downloaded: boolean;
    downloaded_bytes: number;
    total_bytes: number;
    completed_files: number;
    total_files: number;
    message?: string;
    error_code?: string;
    created_at: string;
    updated_at: string;
    finished_at?: string;
}

export interface ModelDownloadList { downloads: ModelDownload[]; available?: boolean; reason?: string }
export interface ModelDownloadEvent { type: 'model_download_changed' | 'model_downloads_changed'; job?: ModelDownload }
export const downloadQueryKey = ['modelDownloads'];

export function activeDownload(job: ModelDownload) {
    return job.state === 'queued' || job.state === 'downloading' || job.state === 'canceling';
}

export function updateDownloadList(list: ModelDownloadList | undefined, job: ModelDownload): ModelDownloadList {
    const existing = list?.downloads.find(item => item.id === job.id);
    if (existing && !activeDownload(existing) && activeDownload(job)) return list!;
    if (existing && Date.parse(existing.updated_at) > Date.parse(job.updated_at)) return list!;
    return { ...list, downloads: [job, ...(list?.downloads ?? []).filter(item => item.id !== job.id)]
        .sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id)).slice(0, 70) };
}

export function downloadProgress(job: ModelDownload): number | null {
    if (job.state === 'completed') return 100;
    if (job.total_bytes <= 0 || job.downloaded_bytes < 0 || job.downloaded_bytes > job.total_bytes) return null;
    return Math.min(99, Math.floor(job.downloaded_bytes / job.total_bytes * 100));
}

export function downloadStage(job: ModelDownload): string {
    if (job.state === 'queued') return 'Waiting to download';
    if (job.state === 'canceling') return 'Canceling…';
    if (job.state === 'completed') return 'Downloaded';
    if (job.state === 'failed') return 'Download failed';
    if (job.state === 'canceled') return 'Canceled';
    if (job.phase === 'preparing') return 'Preparing download';
    if (job.phase === 'checking') return 'Checking files';
    if (job.phase === 'verifying') return 'Verifying files';
    return 'Downloading';
}

export function downloadEvent(value: unknown): ModelDownloadEvent | null {
    if (!value || typeof value !== 'object' || !('type' in value)) return null;
    if (value.type === 'model_downloads_changed') return { type: value.type };
    if (value.type !== 'model_download_changed' || !('job' in value) || !value.job || typeof value.job !== 'object') return null;
    const job = value.job as Partial<ModelDownload>;
    if (typeof job.id !== 'string' || typeof job.model_id !== 'string' || typeof job.name !== 'string' || typeof job.state !== 'string' ||
        !['queued', 'downloading', 'canceling', 'completed', 'failed', 'canceled'].includes(job.state) || typeof job.created_at !== 'string' || typeof job.updated_at !== 'string' ||
        ![job.downloaded_bytes, job.total_bytes, job.completed_files, job.total_files].every(n => typeof n === 'number' && Number.isFinite(n) && n >= 0)) return null;
    return { type: value.type, job: job as ModelDownload };
}

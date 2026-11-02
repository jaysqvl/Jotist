import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Check, ChevronDown, ChevronUp, Download, Loader2, Search, X } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { inputClassName } from '@/components/transcription/FormHelpers';
import { useAuth } from '@/features/auth/hooks/useAuth';
import { modelStorageResponse, storageSize } from './modelStorage';
import type { ModelInventory } from './modelStorage';
import { activeDownload, downloadProgress, downloadQueryKey, downloadStage, updateDownloadList } from './modelDownloads';
import type { DownloadableModel, ModelDownload, ModelDownloadList } from './modelDownloads';

function useDownloads() {
    const { getAuthHeaders } = useAuth();
    const client = useQueryClient();
    return useQuery<ModelDownloadList>({
        queryKey: downloadQueryKey,
        queryFn: async ({ signal }) => {
            const result = await modelStorageResponse<ModelDownloadList>(await fetch('/api/v1/settings/models/downloads', { headers: getAuthHeaders(), signal }));
            // A response begun before an SSE terminal event must not restore
            // stale "downloading" state when that HTTP response arrives later.
            let merged = result;
            for (const job of client.getQueryData<ModelDownloadList>(downloadQueryKey)?.downloads ?? []) merged = updateDownloadList(merged, job);
            return merged;
        },
        retry: 1,
    });
}

function useStartDownload() {
    const { getAuthHeaders } = useAuth();
    const client = useQueryClient();
    return useMutation({
        mutationFn: async ({ id, keep }: { id: string; keep: boolean }) => modelStorageResponse<ModelDownload>(await fetch('/api/v1/settings/models/downloads', {
            method: 'POST', headers: { ...getAuthHeaders(), 'Content-Type': 'application/json' }, body: JSON.stringify({ model_id: id, keep_downloaded: keep }),
        })),
        onSuccess: job => client.setQueryData<ModelDownloadList>(downloadQueryKey, list => updateDownloadList(list, job)),
        onSettled: () => { void client.invalidateQueries({ queryKey: downloadQueryKey }); },
    });
}

export function ModelDownloadPicker({ inventory }: { inventory?: ModelInventory }) {
    const { getAuthHeaders } = useAuth();
    const [open, setOpen] = useState(false);
    const [search, setSearch] = useState('');
    const [purpose, setPurpose] = useState('all');
    const [kept, setKept] = useState<Record<string, boolean>>({});
    const [notice, setNotice] = useState('');
    const jobs = useDownloads();
    const start = useStartDownload();
    const catalog = useQuery<{ models: DownloadableModel[] }>({
        queryKey: ['modelDownloadCatalog'], enabled: open,
        queryFn: async ({ signal }) => modelStorageResponse<{ models: DownloadableModel[] }>(await fetch('/api/v1/settings/models/catalog', { headers: getAuthHeaders(), signal })),
        staleTime: 5 * 60_000, retry: 1,
    });
    const needle = search.trim().toLocaleLowerCase();
    const models = catalog.data?.models.filter(model => (!needle || `${model.name} ${model.source}`.toLocaleLowerCase().includes(needle)) &&
        (purpose === 'all' || (purpose === 'dependencies' ? model.purpose.includes('dependency') : model.purpose.toLocaleLowerCase().startsWith(purpose)))) ?? [];
    const active = new Map(jobs.data?.downloads.filter(activeDownload).map(job => [job.model_id, job]));
    const cached = new Map(inventory?.entries.map(entry => [entry.id, entry]));
    const names = new Map(catalog.data?.models.map(model => [model.source, model.name]));
    return <>
        <Button size="sm" onClick={() => setOpen(true)}><Download className="h-4 w-4" />Download models</Button>
        <Dialog open={open} onOpenChange={setOpen}>
            <DialogContent className="flex max-h-[90dvh] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl">
                <DialogHeader className="space-y-2 p-5 pr-12 sm:p-6">
                    <DialogTitle>Download models</DialogTitle>
                    <DialogDescription>Get model files ready before a run. Downloads continue when you close this window and use no GPU memory.</DialogDescription>
                </DialogHeader>
                <div className="grid min-w-0 gap-2 border-y border-[var(--border-subtle)] p-4 sm:grid-cols-[minmax(0,1fr)_auto]">
                    <div className="relative min-w-0"><Search className="absolute left-3 top-3 h-4 w-4 text-[var(--text-tertiary)]" aria-hidden="true" /><Input autoFocus aria-label="Search models to download" placeholder="Search models…" className={`${inputClassName} pl-9`} value={search} onChange={event => setSearch(event.target.value)} /></div>
                    <select aria-label="Filter models to download" className={`${inputClassName} min-h-10 px-3`} value={purpose} onChange={event => setPurpose(event.target.value)}><option value="all">All models</option><option value="recognition">Transcription</option><option value="speakers">Speakers</option><option value="word timing">Word timing</option><option value="dependencies">Shared dependencies</option></select>
                </div>
                {notice && <p role="status" className="px-5 pt-3 text-sm text-[var(--success-solid)]">{notice}</p>}
                {start.isError && <p role="alert" className="px-5 pt-3 text-sm text-[var(--error-solid)]">{start.error.message} Check Downloads below before trying again.</p>}
                <div className="min-h-0 overflow-y-auto overscroll-contain">
                    {catalog.isPending ? <p role="status" className="flex items-center justify-center gap-2 p-8 text-sm"><Loader2 className="h-4 w-4 animate-spin" />Loading models…</p> : catalog.isError ? <p role="alert" className="p-5 text-sm text-[var(--error-solid)]">{catalog.error.message}</p> : !models.length ? <p className="p-8 text-center text-sm text-[var(--text-secondary)]">No models match these filters.</p> : <ul className="divide-y divide-[var(--border-subtle)]">
                        {models.map(model => {
                            const stored = cached.get(model.id);
                            const job = active.get(model.id);
                            const keep = stored?.keep_downloaded || kept[model.id] || false;
                            return <li key={model.id} className="grid min-w-0 gap-3 p-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:p-5">
                                <div className="min-w-0"><h3 className="break-words text-sm font-medium">{model.name}</h3><p className="mt-1 break-all text-xs text-[var(--text-secondary)]">{model.purpose} · {model.source}</p>
                                    {!!model.dependencies.length && <p className="mt-2 text-xs leading-5 text-[var(--text-secondary)]">Includes {model.dependencies.map(source => names.get(source) ?? (source === 'jotist/whisperx-vad' ? 'speech detection' : source)).join(' · ')}</p>}
                                    {model.gated && <p className="mt-1 text-xs leading-5 text-[var(--text-secondary)]">Requires a Hugging Face token and accepted model access terms.</p>}
                                    {stored && <p className="mt-2 flex items-center gap-1 text-xs text-[var(--success-solid)]"><Check className="h-3 w-3" />{stored.status === 'partial' ? 'Some files cached' : `${storageSize(stored.bytes)} cached`}</p>}
                                </div>
                                <div className="flex flex-wrap items-center justify-between gap-3 sm:flex-col sm:items-end sm:justify-center">
                                    <Button variant={stored ? 'outline' : 'default'} size="sm" disabled={!!job || start.isPending || jobs.isError || jobs.isPending || jobs.data?.available === false} aria-label={`Download ${model.name}`} onClick={() => {
                                        setNotice(''); start.mutate({ id: model.id, keep }, { onSuccess: () => setNotice(`${model.name} added to downloads.`) });
                                    }}>{job ? <><Loader2 className="h-4 w-4 animate-spin" />{downloadStage(job)}</> : <><Download className="h-4 w-4" />{stored ? 'Check download' : 'Download'}</>}</Button>
                                    <label htmlFor={`keep-download-${model.id}`} className="flex min-h-8 cursor-pointer items-center gap-2 text-xs text-[var(--text-secondary)]"><Switch id={`keep-download-${model.id}`} aria-label={`Keep ${model.name} after download`} checked={keep} disabled={!!stored?.keep_downloaded || !!job || start.isPending} onCheckedChange={value => setKept(values => ({ ...values, [model.id]: value }))} />Keep downloaded</label>
                                </div>
                            </li>;
                        })}
                    </ul>}
                </div>
                <div className="space-y-2 border-t border-[var(--border-subtle)] p-4 text-xs leading-5 text-[var(--text-secondary)]">
                    {jobs.isError && <p role="alert" className="text-[var(--error-solid)]">Download status is unavailable. Refresh before adding another download.</p>}
                    {jobs.data?.reason && <p role="status" className="text-[var(--warning-solid)]">{jobs.data.reason}</p>}
                    <p>Keep downloaded protects this model and its included dependencies from cleanup. Other downloads follow your retention policy. Runtime packages and automatically selected language files may still prepare on first use.</p>
                </div>
            </DialogContent>
        </Dialog>
    </>;
}

export function ModelDownloadActivity() {
    const { getAuthHeaders } = useAuth();
    const client = useQueryClient();
    const jobs = useDownloads();
    const retry = useStartDownload();
    const [all, setAll] = useState(false);
    const cancel = useMutation({
        mutationFn: async (id: string) => modelStorageResponse<ModelDownload>(await fetch(`/api/v1/settings/models/downloads/${encodeURIComponent(id)}/cancel`, { method: 'POST', headers: getAuthHeaders() })),
        onSuccess: job => client.setQueryData<ModelDownloadList>(downloadQueryKey, list => updateDownloadList(list, job)),
        onSettled: () => { void client.invalidateQueries({ queryKey: downloadQueryKey }); },
    });
    const downloads = jobs.data?.downloads ?? [];
    if (!downloads.length) return jobs.isError || jobs.data?.reason ? <p role="status" className="text-sm text-[var(--warning-solid)]">{jobs.data?.reason ?? 'Download status is unavailable. Refresh the page to reconnect.'}</p> : null;
    const running = downloads.filter(activeDownload).sort((a, b) => Number(a.state === 'queued') - Number(b.state === 'queued') || a.created_at.localeCompare(b.created_at));
    const ordered = [...running, ...downloads.filter(job => !activeDownload(job))];
    const visible = all ? ordered : ordered.slice(0, 3);
    return <section aria-labelledby="model-downloads-heading" className="min-w-0 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-card)]">
        <div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--border-subtle)] p-4"><h3 id="model-downloads-heading" className="text-sm font-medium">Downloads{running.length > 0 && <span className="ml-2 text-xs text-[var(--text-secondary)]">{running.length} active or waiting</span>}</h3><span className="text-xs text-[var(--text-tertiary)]">Model files only</span></div>
        {(cancel.isError || retry.isError) && <p role="alert" className="px-4 pt-3 text-sm text-[var(--error-solid)]">{cancel.error?.message ?? retry.error?.message}</p>}
        <ul className="divide-y divide-[var(--border-subtle)]">{visible.map(job => {
            const percentage = downloadProgress(job);
            return <li key={job.id} className="min-w-0 p-4"><div className="flex flex-wrap items-start justify-between gap-2"><div className="min-w-0"><p className="break-words text-sm font-medium">{job.name}</p><p className={`mt-1 text-xs ${job.state === 'failed' ? 'text-[var(--error-solid)]' : 'text-[var(--text-secondary)]'}`}>{downloadStage(job)}{job.keep_downloaded ? job.state === 'completed' ? ' · Kept downloaded' : activeDownload(job) ? ' · Keep when ready' : '' : ''}</p></div>
                {activeDownload(job) && job.can_cancel ? <Button variant="ghost" size="sm" aria-label={`Cancel ${job.name} download`} disabled={job.state === 'canceling' || cancel.isPending || jobs.data?.available === false} onClick={() => cancel.mutate(job.id)}><X className="h-4 w-4" />Cancel</Button> : (job.state === 'failed' || job.state === 'canceled') && !running.some(other => other.model_id === job.model_id) ? <Button variant="outline" size="sm" disabled={retry.isPending || jobs.isError || jobs.data?.available === false} onClick={() => retry.mutate({ id: job.model_id, keep: job.keep_downloaded })}>Try again</Button> : null}</div>
                {job.state === 'downloading' && <div className="mt-3 space-y-1.5"><div role="progressbar" aria-label={`${job.name} download progress`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percentage ?? undefined} className="h-1.5 overflow-hidden rounded-full bg-[var(--bg-secondary)]"><div className={`h-full rounded-full bg-[var(--brand-solid)] ${percentage === null ? 'animate-pulse' : ''}`} style={{ width: percentage === null ? '25%' : `${percentage}%` }} /></div><p className="text-xs tabular-nums text-[var(--text-secondary)]">{job.total_bytes > 0 ? `${storageSize(job.downloaded_bytes)} / ${storageSize(job.total_bytes)}` : 'Preparing files…'}{job.total_files > 0 ? ` · ${job.completed_files}/${job.total_files} files` : ''}</p></div>}
                {job.message && !activeDownload(job) && <p className="mt-2 text-xs leading-5 text-[var(--text-secondary)]">{job.message}</p>}
            </li>;
        })}</ul>
        {ordered.length > 3 && <Button variant="ghost" size="sm" className="m-2" onClick={() => setAll(!all)}>{all ? 'Show less' : `Show all ${ordered.length} downloads`}{all ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}</Button>}
    </section>;
}

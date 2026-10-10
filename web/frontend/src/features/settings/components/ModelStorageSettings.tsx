import { useEffect, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ChevronDown, ChevronUp, HardDrive, Loader2, RefreshCw, Search, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { inputClassName } from '@/components/transcription/FormHelpers';
import { useAuth } from '@/features/auth/hooks/useAuth';
import { modelStatus, modelStorageResponse, queuedRemovalWarning, storageSize, visibleModels } from './modelStorage';
import type { CachedModel, ModelFilter, ModelInventory, ModelSort } from './modelStorage';
import { watchModelStorage } from './modelStorageEvents';

const panel = 'min-w-0 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-card)]';

export function ModelStorageSettings() {
    const { getAuthHeaders, token } = useAuth();
    const queryClient = useQueryClient();
    const [search, setSearch] = useState('');
    const [sort, setSort] = useState<ModelSort>('size');
    const [filter, setFilter] = useState<ModelFilter>('all');
    const [details, setDetails] = useState<string | null>(null);
    const [otherOpen, setOtherOpen] = useState(false);
    const [selected, setSelected] = useState<CachedModel | null>(null);
    const [notice, setNotice] = useState('');
    const [liveDisconnected, setLiveDisconnected] = useState(false);
    const removalTrigger = useRef<HTMLButtonElement | null>(null);
    const searchInput = useRef<HTMLInputElement | null>(null);
    useEffect(() => {
        if (!token) return;
        setLiveDisconnected(false);
        return watchModelStorage(token,
            () => { void queryClient.invalidateQueries({ queryKey: ['modelStorage'] }); },
            connected => setLiveDisconnected(!connected));
    }, [token, queryClient]);
    const query = useQuery<ModelInventory>({
        queryKey: ['modelStorage'],
        queryFn: async ({ signal }) => modelStorageResponse<ModelInventory>(await fetch('/api/v1/settings/models', { headers: getAuthHeaders(), signal })),
        retry: 1,
    });
    const refresh = useMutation({
        mutationFn: async () => modelStorageResponse<ModelInventory>(await fetch('/api/v1/settings/models?refresh=true', { headers: getAuthHeaders() })),
        onSuccess: data => queryClient.setQueryData(['modelStorage'], data),
    });
    const remove = useMutation({
        mutationFn: async (model: CachedModel) => modelStorageResponse<{ message: string }>(await fetch(`/api/v1/settings/models/${encodeURIComponent(model.id)}`, {
            method: 'DELETE', headers: { ...getAuthHeaders(), 'Content-Type': 'application/json' }, body: JSON.stringify({ version: model.version }),
        })),
        onSuccess: data => { removalTrigger.current = null; setSelected(null); setNotice(data.message); },
        onSettled: () => { void queryClient.invalidateQueries({ queryKey: ['modelStorage'] }); },
    });
    const inventory = query.data;
    const models = visibleModels(inventory?.entries ?? [], search, filter, sort);
    const current = inventory?.entries.find(model => model.id === selected?.id);
    const cachedOnlyProfiles = current?.cached_only_profiles ?? selected?.cached_only_profiles ?? 0;
    const queuedWarning = queuedRemovalWarning(current ?? selected ?? undefined);
    const canConfirm = !!selected && !!current?.can_remove && current.version === selected.version && !query.isError && !remove.isPending;
    const free = inventory?.volumes.reduce((total, volume) => total + volume.available_bytes, 0) ?? 0;
    const scanned = inventory && new Date(inventory.scanned_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
    const loading = query.isPending;
    const error = refresh.isError ? refresh.error.message : query.isError ? query.error.message : '';

    return <div className="min-w-0 space-y-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0"><h2 className="flex items-center gap-2 text-lg font-display font-semibold"><HardDrive className="h-5 w-5 text-[var(--brand-solid)]" />Model storage</h2>
                <p className="mt-1 max-w-2xl text-sm leading-6 text-[var(--text-secondary)]">Manage downloaded models on this server. Profiles stay saved, and missing files download when a model is used again.</p></div>
            <Button variant="outline" size="sm" disabled={loading || refresh.isPending || remove.isPending} onClick={() => refresh.mutate()}>
                <RefreshCw className={`h-4 w-4 ${refresh.isPending ? 'animate-spin' : ''}`} />Refresh
            </Button>
        </div>
        <div className="grid min-w-0 gap-3 sm:grid-cols-3">
            <Metric title="Model files" value={inventory ? storageSize(inventory.model_bytes) : '—'} note="Shared files counted once" />
            <Metric title="Cached models" value={inventory ? String(inventory.entries.length) : '—'} note="Recognition, timing and speakers" />
            <Metric title="Available space" value={inventory?.volumes.length ? storageSize(free) : '—'} note={inventory && inventory.volumes.length > 1 ? `Across ${inventory.volumes.length} storage volumes` : 'On the cache storage volume'} />
        </div>
        {notice && <p role="status" className="rounded-xl bg-[var(--success-translucent)] p-3 text-sm text-[var(--success-solid)]">{notice}</p>}
        {error && <p role="alert" className="rounded-xl bg-[var(--error-translucent)] p-3 text-sm text-[var(--error-solid)]">{error} Existing data is shown until the next successful refresh.</p>}
        {liveDisconnected && <p role="status" className="text-sm text-[var(--warning-solid)]">Live updates are disconnected. Use Refresh to check the latest files and run activity.</p>}
        {inventory?.warnings.map((warning, i) => <p role="status" key={i} className="text-sm text-[var(--warning-solid)]">{warning}</p>)}
        <section className={panel} aria-labelledby="downloaded-models-heading">
            <div className="space-y-3 border-b border-[var(--border-subtle)] p-4 sm:p-5">
                <div className="flex flex-wrap items-center justify-between gap-2"><h3 id="downloaded-models-heading" className="font-medium">Downloaded models</h3><span className="text-xs text-[var(--text-tertiary)]">{scanned ? `Full check ${scanned}` : 'Checking files…'}</span></div>
                <div className="grid min-w-0 gap-2 sm:grid-cols-[minmax(0,1fr)_auto_auto]">
                    <div className="relative min-w-0"><Search aria-hidden="true" className="absolute left-3 top-3 h-4 w-4 text-[var(--text-tertiary)]" /><Input ref={searchInput} aria-label="Search downloaded models" placeholder="Search models…" value={search} onChange={event => setSearch(event.target.value)} className={`${inputClassName} pl-9`} /></div>
                    <select aria-label="Filter downloaded models" value={filter} onChange={event => setFilter(event.target.value as ModelFilter)} className={`${inputClassName} min-h-10 w-full px-3`}><option value="all">All models</option><option value="available">Can remove</option><option value="busy">In use or downloading</option></select>
                    <select aria-label="Sort downloaded models" value={sort} onChange={event => setSort(event.target.value as ModelSort)} className={`${inputClassName} min-h-10 w-full px-3`}><option value="size">Largest first</option><option value="name">Name</option><option value="last_used">Last used</option></select>
                </div>
            </div>
            {loading ? <p role="status" className="flex items-center justify-center gap-2 p-8 text-sm text-[var(--text-secondary)]"><Loader2 className="h-4 w-4 animate-spin" />Inspecting model files…</p> : !inventory ? <p className="p-6 text-sm text-[var(--text-secondary)]">Model storage could not be loaded. Try Refresh.</p> : models.length === 0 ? <p className="p-6 text-sm text-[var(--text-secondary)]">{inventory.entries.length ? 'No models match these filters.' : 'No downloaded model files found. Models appear here after their first download.'}</p> : <div className="divide-y divide-[var(--border-subtle)]">
                {models.map(model => <div key={model.id} className="min-w-0 p-4 sm:p-5">
                    <div className="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
                        <div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><h4 className="break-words font-medium">{model.name}</h4><span className={`rounded-full px-2 py-0.5 text-xs ${model.uses.length ? 'bg-[var(--brand-light)] text-accent-foreground' : 'bg-[var(--bg-secondary)] text-[var(--text-secondary)]'}`}>{modelStatus(model)}</span></div>
                            <p className="mt-1 text-sm text-[var(--text-secondary)]">{model.purpose} · {storageSize(model.bytes)}</p>
                            {model.uses.length > 0 ? <p className="mt-1 text-xs leading-5 text-[var(--text-secondary)]">{model.uses[0].state === 'queued' ? 'Queued run: ' : model.uses[0].state === 'in_use' ? 'Active run: ' : 'Setup: '}{model.uses[0].recording_title || model.uses[0].stage || 'Preparing model files'}{model.uses[0].recording_title && model.uses[0].stage ? ` · ${model.uses[0].stage}` : ''}{model.uses.length > 1 ? ` · ${model.uses.length - 1} more` : ''}</p> : !model.can_remove && <p className="mt-1 text-xs leading-5 text-[var(--text-secondary)]">{model.reason}</p>}
                        </div>
                        <div className="flex flex-wrap items-center gap-2 sm:justify-end">
                            <Button variant="ghost" size="sm" aria-expanded={details === model.id} aria-controls={`model-details-${model.id}`} onClick={() => setDetails(details === model.id ? null : model.id)}>Details{details === model.id ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}</Button>
                            <Button variant="outline" size="sm" disabled={!model.can_remove || remove.isPending || query.isError} aria-label={`Remove ${model.name}`} onClick={event => { removalTrigger.current = event.currentTarget; setSelected(model); setNotice(''); remove.reset(); }}><Trash2 className="h-4 w-4" />{model.status === 'removal_pending' ? 'Finish cleanup' : 'Remove'}</Button>
                        </div>
                    </div>
                    {details === model.id && <div id={`model-details-${model.id}`} className="mt-3 space-y-2 rounded-xl bg-[var(--bg-main)] p-3 text-xs leading-5 text-[var(--text-secondary)]">
                        <p className="break-all">Source: {model.source}</p>
                        <p>{model.revisions > 0 ? `${model.revisions} cached revision${model.revisions === 1 ? '' : 's'} · ` : ''}{model.locations} cache location{model.locations === 1 ? '' : 's'} · {model.profiles} saved profile{model.profiles === 1 ? '' : 's'}</p>
                        <p>Last matching run: {model.last_used ? new Date(model.last_used).toLocaleString() : 'No recorded use'}</p>
                        <p>References follow saved and run configurations. Automatically selected timing and speaker dependencies may be shared.</p>
                        <p>Estimated space freed: {storageSize(model.removable_bytes)}{model.shared_bytes > 0 ? ` · ${storageSize(model.shared_bytes)} shared with other files` : ''}. Filesystem compression and shared storage may change the amount freed.</p>
                    </div>}
                </div>)}
            </div>}
            <p className="border-t border-[var(--border-subtle)] p-4 text-xs leading-5 text-[var(--text-tertiary)]">Updates with model activity. Refresh checks all files. Active runs and downloads prevent removal; queued runs can download missing files when they start. GPU memory usage is separate.</p>
        </section>
        <section className={panel}><button className="flex min-h-14 w-full items-center justify-between gap-3 p-4 text-left" aria-expanded={otherOpen} aria-controls="other-model-storage" onClick={() => setOtherOpen(!otherOpen)}><span><span className="block text-sm font-medium">Other runtime and download storage</span><span className="mt-1 block text-xs text-[var(--text-secondary)]">{inventory ? storageSize(inventory.runtime_bytes + inventory.package_cache_bytes) : '—'} · kept separately</span></span>{otherOpen ? <ChevronUp className="h-4 w-4 shrink-0" /> : <ChevronDown className="h-4 w-4 shrink-0" />}</button>
            {otherOpen && <div id="other-model-storage" className="space-y-2 border-t border-[var(--border-subtle)] p-4 text-sm text-[var(--text-secondary)]"><p>Installed runtimes: {inventory ? storageSize(inventory.runtime_bytes) : '—'}</p><p>Package and download caches: {inventory ? storageSize(inventory.package_cache_bytes) : '—'}</p><p className="text-xs leading-5">These are audited here and kept when you remove a model. Recordings, transcripts and profiles are stored separately.</p></div>}
        </section>
        <Dialog open={!!selected} onOpenChange={open => { if (!open && !remove.isPending) setSelected(null); }}>
            <DialogContent className="max-h-[90dvh] overflow-y-auto" showCloseButton={!remove.isPending} onCloseAutoFocus={event => {
                event.preventDefault();
                if (removalTrigger.current?.isConnected && !removalTrigger.current.disabled) removalTrigger.current.focus();
                else searchInput.current?.focus();
            }} onEscapeKeyDown={event => { if (remove.isPending) event.preventDefault(); }} onInteractOutside={event => { if (remove.isPending) event.preventDefault(); }}>
                <DialogHeader><DialogTitle className="break-words">Remove {selected?.name}?</DialogTitle><DialogDescription>Remove its downloaded files{selected && selected.revisions > 0 ? `, including all ${selected.revisions} cached revision${selected.revisions === 1 ? '' : 's'}` : ''}. Saved profiles, recordings and completed runs stay intact.</DialogDescription></DialogHeader>
                <div className="space-y-3 text-sm leading-6"><p>Estimated space freed: <strong>{storageSize(selected?.removable_bytes ?? 0)}</strong></p>
                    <p className="text-[var(--text-secondary)]">Runs with downloading enabled fetch missing files before processing. They need network access and any required model access token.</p>
                    {queuedWarning && <p className="rounded-lg bg-[var(--warning-translucent)] p-3 text-[var(--warning-solid)]">{queuedWarning}</p>}
                    {cachedOnlyProfiles > 0 && <p className="rounded-lg bg-[var(--warning-translucent)] p-3 text-[var(--warning-solid)]">{cachedOnlyProfiles} saved profile{cachedOnlyProfiles === 1 ? ' uses' : 's use'} cached files only. Enable downloading before using {cachedOnlyProfiles === 1 ? 'that profile' : 'those profiles'} again.</p>}
                    {selected && (!current || current.version !== selected.version) && <p role="alert" className="text-[var(--warning-solid)]">The files changed. Close this dialog and review the updated inventory.</p>}
                    {current && !current.can_remove && <p role="alert" className="text-[var(--warning-solid)]">{current.reason}</p>}
                    {remove.isError && <p role="alert" className="text-[var(--error-solid)]">{remove.error.message}</p>}
                </div>
                <DialogFooter><Button variant="outline" disabled={remove.isPending} onClick={() => setSelected(null)}>Cancel</Button><Button variant="destructive" disabled={!canConfirm} onClick={() => { if (selected) remove.mutate(selected); }}>{remove.isPending ? <><Loader2 className="h-4 w-4 animate-spin" />Removing files…</> : 'Remove model files'}</Button></DialogFooter>
            </DialogContent>
        </Dialog>
    </div>;
}

function Metric({ title, value, note }: { title: string; value: string; note: string }) {
    return <div className={`${panel} p-4`}><p className="text-xs text-[var(--text-secondary)]">{title}</p><p className="my-1 text-2xl font-semibold tabular-nums">{value}</p><p className="text-xs leading-5 text-[var(--text-tertiary)]">{note}</p></div>;
}

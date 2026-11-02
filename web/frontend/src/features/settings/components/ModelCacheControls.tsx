import { useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Loader2, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { inputClassName } from '@/components/transcription/FormHelpers';
import { useAuth } from '@/features/auth/hooks/useAuth';
import { modelStorageResponse, storageSize } from './modelStorage';
import type { ModelCleanupPlan, ModelCleanupResult, ModelInventory, ModelRetentionPolicy } from './modelStorage';

const panel = 'min-w-0 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-card)]';

export function ModelCacheControls({ policy, unavailable, onNotice }: {
    policy?: ModelRetentionPolicy;
    unavailable: boolean;
    onNotice: (message: string) => void;
}) {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();
    const [draft, setDraft] = useState<{ enabled: boolean; days: string } | null>(null);
    const [plan, setPlan] = useState<ModelCleanupPlan | null>(null);
    const clearTrigger = useRef<HTMLButtonElement | null>(null);
    const enabled = draft?.enabled ?? policy?.enabled ?? true;
    const daysText = draft?.days ?? String(policy?.retention_days ?? 14);
    const days = Number(daysText);
    const valid = Number.isInteger(days) && days >= 1 && days <= 365;
    const dirty = !!policy && (enabled !== policy.enabled || daysText !== String(policy.retention_days));
    const change = (values: Partial<{ enabled: boolean; days: string }>) => {
        setDraft({ enabled, days: daysText, ...values });
        save.reset();
    };
    const save = useMutation({
        mutationFn: async () => modelStorageResponse<ModelRetentionPolicy>(await fetch('/api/v1/settings/models/retention', {
            method: 'PUT', headers: { ...getAuthHeaders(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ enabled, retention_days: days }),
        })),
        onSuccess: value => {
            setDraft(null);
            queryClient.setQueryData<ModelInventory>(['modelStorage'], old => old ? { ...old, retention: value } : old);
            void queryClient.invalidateQueries({ queryKey: ['modelStorage'] });
        },
    });
    const preview = useMutation({
        mutationFn: async () => modelStorageResponse<ModelCleanupPlan>(await fetch('/api/v1/settings/models/cleanup?mode=all', { headers: getAuthHeaders() })),
        onSuccess: value => { setPlan(value); cleanup.reset(); },
    });
    const cleanup = useMutation({
        mutationFn: async (value: ModelCleanupPlan) => modelStorageResponse<ModelCleanupResult>(await fetch('/api/v1/settings/models/cleanup', {
            method: 'POST', headers: { ...getAuthHeaders(), 'Content-Type': 'application/json' },
            body: JSON.stringify({ mode: value.mode, version: value.version }),
        })),
        onSuccess: value => { setPlan(null); if (!value.failed_models) onNotice(value.message); },
        onSettled: () => { void queryClient.invalidateQueries({ queryKey: ['modelStorage'] }); },
    });
    if (!policy) return null;
    const pending = save.isPending || cleanup.isPending;
    const lastCheck = policy.last_checked_at ? new Date(policy.last_checked_at).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : null;

    return <>
        <section className={panel} aria-labelledby="cache-retention-title">
            <div className="space-y-4 p-4 sm:p-5">
                <div className="flex items-start justify-between gap-4">
                    <div className="min-w-0"><h3 id="cache-retention-title" className="font-medium">Automatic cleanup</h3>
                        <p className="mt-1 text-sm leading-6 text-[var(--text-secondary)]">Free up space by removing downloaded models you haven’t used recently.</p></div>
                    <Switch aria-label="Automatic model cleanup" checked={enabled} disabled={unavailable || pending} onCheckedChange={value => change({ enabled: value })} />
                </div>
                <div className="flex flex-wrap items-end gap-3">
                    <label className="flex flex-wrap items-center gap-2 text-sm" htmlFor="model-retention-days">Keep unused models for
                        <Input id="model-retention-days" aria-label="Model retention days" aria-describedby="cache-retention-help" type="number" inputMode="numeric" min={1} max={365} step={1} value={daysText} onChange={event => change({ days: event.target.value })} disabled={unavailable || pending} className={`${inputClassName} w-20`} />
                        <span>days</span></label>
                    <Button variant="outline" size="sm" disabled={unavailable || pending || !dirty || !valid} onClick={() => save.mutate()}>{save.isPending ? <><Loader2 className="h-4 w-4 animate-spin" />Saving…</> : 'Save changes'}</Button>
                    {save.isSuccess && !dirty && <span role="status" className="text-xs text-[var(--success-solid)]">Saved</span>}
                </div>
                <p id="cache-retention-help" className="text-xs leading-5 text-[var(--text-secondary)]">Checked daily. Active runs, downloads and cached-only profiles are protected. Missing models download again when needed.</p>
                {dirty && !valid && <p role="alert" className="text-sm text-[var(--error-solid)]">Choose a whole number from 1 to 365 days.</p>}
                {save.isError && <p role="alert" className="text-sm text-[var(--error-solid)]">{save.error.message}</p>}
                {lastCheck && <p className="text-xs text-[var(--text-tertiary)]">Last cleanup check {lastCheck}{policy.last_removed_models > 0 ? ` · ${policy.last_removed_models} models removed` : ''}{policy.last_failed_models > 0 ? ' · Some removals need attention' : ''}</p>}
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 border-t border-[var(--border-subtle)] p-4 sm:px-5">
                <div className="min-w-0"><p className="text-sm font-medium">Start fresh</p><p className="mt-1 text-xs leading-5 text-[var(--text-secondary)]">Clear downloaded model files and keep your profiles and completed runs.</p></div>
                <Button ref={clearTrigger} variant="outline" size="sm" disabled={unavailable || pending || preview.isPending} onClick={() => preview.mutate()}>{preview.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Trash2 className="h-4 w-4" />}Clear downloaded models</Button>
                {preview.isError && <p role="alert" className="w-full text-sm text-[var(--error-solid)]">{preview.error.message}</p>}
                {cleanup.data && cleanup.data.failed_models > 0 && <p role="alert" className="w-full text-sm text-[var(--warning-solid)]">{cleanup.data.message}</p>}
            </div>
        </section>
        <Dialog open={!!plan} onOpenChange={open => { if (!open && !cleanup.isPending) setPlan(null); }}>
            <DialogContent className="max-h-[90dvh] overflow-y-auto" showCloseButton={!cleanup.isPending} onCloseAutoFocus={event => { event.preventDefault(); clearTrigger.current?.focus(); }} onEscapeKeyDown={event => { if (cleanup.isPending) event.preventDefault(); }} onInteractOutside={event => { if (cleanup.isPending) event.preventDefault(); }}>
                <DialogHeader><DialogTitle>Clear downloaded models?</DialogTitle><DialogDescription>Remove {plan?.models.length ?? 0} cached models. Saved profiles, recordings and completed runs stay intact.</DialogDescription></DialogHeader>
                <div className="space-y-3 text-sm leading-6">
                    <p>Estimated space freed: <strong>{storageSize(plan?.removable_bytes ?? 0)}</strong></p>
                    <p className="text-[var(--text-secondary)]">Models download again the next time they are used. This needs network access and any required model access token. Installed runtimes stay in place.</p>
                    {plan && plan.models.length > 0 && <p className="break-words text-xs text-[var(--text-tertiary)]">{plan.models.slice(0, 4).map(model => model.name).join(' · ')}{plan.models.length > 4 ? ` · and ${plan.models.length - 4} more` : ''}</p>}
                    {!!plan?.skipped_models && <p className="rounded-lg bg-[var(--brand-light)] p-3 text-[var(--text-primary)]">{plan.skipped_models} protected models will be kept.</p>}
                    {plan?.cached_only_profiles && <p className="rounded-lg bg-[var(--warning-translucent)] p-3 text-[var(--warning-solid)]">Some saved profiles use cached files only. Enable downloading in those profiles before using them again after clearing.</p>}
                    {cleanup.isError && <p role="alert" className="text-[var(--error-solid)]">{cleanup.error.message}</p>}
                </div>
                <DialogFooter><Button variant="outline" disabled={cleanup.isPending} onClick={() => setPlan(null)}>Cancel</Button><Button variant="destructive" disabled={cleanup.isPending || !plan?.models.length} onClick={() => { if (plan) cleanup.mutate(plan); }}>{cleanup.isPending ? <><Loader2 className="h-4 w-4 animate-spin" />Clearing…</> : 'Clear model files'}</Button></DialogFooter>
            </DialogContent>
        </Dialog>
    </>;
}

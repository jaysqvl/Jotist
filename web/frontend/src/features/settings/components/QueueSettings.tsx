import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Layers3 } from 'lucide-react';
import { useAuth } from '@/features/auth/hooks/useAuth';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { inputClassName } from '@/components/transcription/FormHelpers';

interface QueueSettingsData {
    workers: number;
    busy_workers: number;
    max_workers: number;
    environment_override: boolean;
}

export function QueueSettings() {
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();
    const [draft, setDraft] = useState<string | null>(null);
    const [notice, setNotice] = useState('');
    const query = useQuery<QueueSettingsData>({
        queryKey: ['queueSettings'],
        queryFn: async () => {
            const response = await fetch('/api/v1/admin/queue/settings', { headers: getAuthHeaders() });
            if (!response.ok) throw new Error('Could not load queue settings.');
            return response.json();
        },
        refetchInterval: 5000,
        retry: 1,
    });
    const save = useMutation({
        mutationFn: async (workers: number): Promise<QueueSettingsData> => {
            const response = await fetch('/api/v1/admin/queue/settings', {
                method: 'PUT', headers: { ...getAuthHeaders(), 'Content-Type': 'application/json' },
                body: JSON.stringify({ workers }),
            });
            const data = await response.json();
            if (!response.ok) throw new Error(data.error ?? 'Could not save queue settings.');
            return data;
        },
        onSuccess: (data) => {
            queryClient.setQueryData(['queueSettings'], data);
            void queryClient.invalidateQueries({ queryKey: ['queueActivity'] });
            setDraft(null);
            setNotice(data.busy_workers > data.workers
                ? 'Saved. Active runs will finish normally before more work starts.'
                : 'Saved. The limit applies across all recordings.');
        },
    });
    const settings = query.data;
    const value = draft ?? String(settings?.workers ?? 1);
    const workers = Number(value);
    const valid = value !== '' && Number.isInteger(workers) && workers >= 1 && workers <= (settings?.max_workers ?? 16);
    const dirty = !!settings && workers !== settings.workers;
    return <section className="min-w-0 space-y-4 rounded-[var(--radius-card)] border border-[var(--border-subtle)] bg-[var(--bg-main)]/50 p-4 sm:p-6" aria-labelledby="queue-settings-heading">
        <div className="flex items-center gap-2"><Layers3 className="h-5 w-5 text-[var(--brand-solid)]" /><h3 id="queue-settings-heading" className="text-lg font-medium">Processing queue</h3></div>
        <p className="text-sm text-[var(--text-secondary)]">One shared queue across recordings. By default, Jotist processes one recording at a time.</p>
        {query.isPending ? <p role="status" className="text-sm text-[var(--text-secondary)]">Loading queue settings…</p> : settings && <fieldset disabled={save.isPending || settings.environment_override} className="min-w-0 space-y-4">
            <div className="grid min-w-0 gap-4 sm:grid-cols-[minmax(0,1fr)_12rem] sm:items-center">
                <div><Label htmlFor="queue-workers" className="text-sm font-medium">Simultaneous recordings</Label>
                    <p id="queue-workers-help" className="mt-1 text-xs leading-5 text-[var(--text-secondary)]">1 is recommended for a single GPU. Parallel recordings share GPU memory. Each recording always runs its own requests in order.</p></div>
                <Input id="queue-workers" type="number" inputMode="numeric" min={1} max={settings.max_workers} step={1} value={value}
                    aria-describedby="queue-workers-help" aria-invalid={!valid} className={inputClassName}
                    onChange={(event) => { setDraft(event.target.value); setNotice(''); save.reset(); }} />
            </div>
            {!valid && <p role="alert" className="text-xs text-[var(--error-solid)]">Enter a whole number from 1 to {settings.max_workers}.</p>}
            {settings.busy_workers > settings.workers && <p className="text-xs text-[var(--text-secondary)]">{settings.busy_workers} active recordings are finishing. New work waits until the saved limit has capacity.</p>}
            {!settings.environment_override && <div className="flex flex-wrap items-center gap-3"><Button onClick={() => save.mutate(workers)} disabled={!dirty || !valid || query.isError} className="w-full sm:w-auto">{save.isPending ? 'Saving…' : 'Save queue settings'}</Button>
                <p className="text-xs leading-5 text-[var(--text-secondary)]">Changes apply immediately. Active runs finish normally.</p></div>}
        </fieldset>}
        {settings?.environment_override && <p className="text-xs leading-5 text-[var(--text-secondary)]">This limit is set by the server’s QUEUE_WORKERS configuration.</p>}
        {(query.isError || save.isError) && <div role="alert" className="flex flex-wrap items-center gap-2 text-sm text-[var(--error-solid)]"><span>{save.isError ? save.error.message : 'Could not refresh queue settings.'}</span>{query.isError && <Button size="sm" variant="outline" onClick={() => void query.refetch()}>Retry</Button>}</div>}
        {notice && <p role="status" className="text-sm text-[var(--success-solid)]">{notice}</p>}
    </section>;
}

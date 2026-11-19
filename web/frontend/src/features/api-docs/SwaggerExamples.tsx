import { useEffect, useRef, useState } from 'react';
import { Play } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';

const examples = {
    list: 'List recordings',
    status: 'Check status',
    transcript: 'Get transcript',
    models: 'Get model catalog',
} as const;
type Example = keyof typeof examples;
type Recording = { id: string; title: string };
type Result = { status: number; json?: string } | 'running' | 'error' | null;
const root = '/api/v1/transcription';
const labelClass = 'grid min-w-0 gap-1.5 text-sm font-medium text-[var(--text-primary)]';
const selectClass = 'h-9 w-full min-w-0 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-main)] px-3 text-base focus-visible:outline-[var(--brand-solid)] md:text-sm';

function validRecordingID(id: string): boolean {
    return /^[A-Za-z0-9_-]{1,128}$/.test(id);
}

function reviewedPath(example: Example, id: string): string | null {
    switch (example) {
        case 'list': return `${root}/list?page=1&limit=10`;
        case 'models': return `${root}/models`;
        case 'status': return validRecordingID(id) ? `${root}/${id}/status` : null;
        // The ordinary transcript endpoint may backfill legacy run history.
        case 'transcript': return validRecordingID(id) ? `${root}/${id}/transcript?read_only=true` : null;
    }
}

function recordingChoices(payload: unknown): Recording[] {
    if (!payload || typeof payload !== 'object' || !('jobs' in payload) || !Array.isArray(payload.jobs)) return [];
    return payload.jobs.flatMap(job => {
        if (!job || typeof job.id !== 'string' || !validRecordingID(job.id)) return [];
        return [{ id: job.id, title: typeof job.title === 'string' && job.title.trim() ? job.title : job.id }];
    });
}

export function SwaggerExamples() {
    const [example, setExample] = useState<Example>('list');
    const [recordings, setRecordings] = useState<Recording[]>([]);
    const [recordingID, setRecordingID] = useState('');
    const [result, setResult] = useState<Result>(null);
    const pending = useRef<AbortController | null>(null);
    useEffect(() => () => pending.current?.abort(), []);

    const needsRecording = example === 'status' || example === 'transcript';
    const selectedRecording = recordings.some(recording => recording.id === recordingID);
    const path = reviewedPath(example, recordingID.trim());

    function clearResult() {
        pending.current?.abort();
        pending.current = null;
        setResult(null);
    }

    function chooseRecording(id: string) {
        clearResult();
        setRecordingID(id);
    }

    async function run() {
        if (!path || pending.current) return;
        const request = new AbortController();
        pending.current = request;
        setResult('running');
        try {
            const response = await fetch(path, {
                method: 'GET', cache: 'no-store', redirect: 'error', credentials: 'omit', signal: request.signal,
            });
            const payload: unknown = await response.json().catch(() => undefined);
            if (request.signal.aborted) return;
            if (response.ok && example === 'list') {
                const choices = recordingChoices(payload);
                setRecordings(choices);
                setRecordingID(current => choices.some(recording => recording.id === current) ? current : choices[0]?.id ?? '');
            }
            setResult({ status: response.status, json: JSON.stringify(payload, null, 2) });
        } catch {
            if (!request.signal.aborted) setResult('error');
        } finally {
            if (pending.current === request) pending.current = null;
        }
    }

    return (
        <div className="border-t border-[var(--border-subtle)] p-4 sm:p-5">
            <div className="grid gap-3 sm:grid-cols-2">
                <label className={labelClass}>
                    Example
                    <select className={selectClass} value={example} onChange={event => {
                        clearResult();
                        setExample(event.currentTarget.value as Example);
                    }}>
                        {Object.entries(examples).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                    </select>
                </label>
                {needsRecording && (
                    <div className="min-w-0 space-y-2">
                        {recordings.length > 0 && (
                            <label className={labelClass}>
                                Recording
                                <select className={selectClass} value={selectedRecording ? recordingID : ''} onChange={event => chooseRecording(event.currentTarget.value)}>
                                    {recordings.map(recording => <option key={recording.id} value={recording.id}>{recording.title}</option>)}
                                    <option value="">Enter a recording ID</option>
                                </select>
                            </label>
                        )}
                        {!selectedRecording && (
                            <label className={labelClass}>
                                Recording ID
                                <Input value={recordingID} onChange={event => chooseRecording(event.currentTarget.value)} autoComplete="off" spellCheck={false} />
                                {!recordings.length && <span className="text-xs font-normal text-[var(--text-secondary)]">Run List recordings to pick a recording by title.</span>}
                            </label>
                        )}
                    </div>
                )}
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-2.5">
                <Button variant="outline" onClick={() => void run()} disabled={!path || result === 'running'}>
                    <Play aria-hidden="true" className="size-4" /> {result === 'running' ? 'Running…' : 'Run'}
                </Button>
                <span className="text-xs text-[var(--text-secondary)]">Runs a read-only request using your Jotist sign-in.</span>
            </div>
            {path && <code className="mt-3 block break-all text-xs text-[var(--text-secondary)]">GET {path}</code>}
            <div className="text-sm" aria-live="polite">
                {result && typeof result === 'object' && <p className="mt-2 font-medium text-[var(--text-primary)]">HTTP {result.status}</p>}
                {result === 'error' && <p className="mt-2 text-[var(--error)]">Could not reach the API.</p>}
            </div>
            {result && typeof result === 'object' && result.json && (
                <details className="mt-2 text-sm text-[var(--text-primary)]">
                    <summary className="cursor-pointer">Response JSON</summary>
                    <pre className="mt-2 max-h-64 overflow-auto rounded-lg bg-[var(--bg-main)] p-3 text-xs"><code>{result.json}</code></pre>
                </details>
            )}
        </div>
    );
}

export interface QueueActivityEntry {
    recording_id: string;
    recording_title: string;
    state: 'running' | 'finishing' | 'resource_wait' | 'retry_wait' | 'waiting_worker' | 'queued' | 'checking';
    has_worker: boolean;
    queue_item_id?: string;
    queued_at?: string;
    execution_id?: string;
    run_number?: number;
    model_family: string;
    model: string;
    profile_name?: string;
    stage?: string;
    stage_number?: number;
    stage_total?: number;
    stage_scope?: 'track';
    stage_state?: string;
    retry_at?: string;
    started_at?: string;
    queued_runs: number;
    queued_jobs?: QueueActivityQueuedJob[];
}

export interface QueueActivityQueuedJob {
    queue_item_id: string;
    queued_at?: string;
    model_family: string;
    model: string;
    profile_name?: string;
}

export interface QueueActivity {
    generated_at: string;
    workers: number;
    busy_workers: number;
    waiting_recordings: number;
    queued_runs: number;
    recordings: QueueActivityEntry[];
}

export interface QueuedActivityRun extends QueueActivityQueuedJob {
    recording_id: string;
    recording_title: string;
    state: 'queued' | 'waiting_worker';
    execution_id?: string;
    run_number?: number;
    waiting_on?: QueueWaitTarget[];
    wait_reason?: 'worker' | 'earlier_run';
}

export type QueueWaitTarget = Pick<QueueActivityEntry, 'recording_id' | 'recording_title' | 'execution_id' | 'run_number'>;

// This is newest-first presentation, independent of each recording's run order.
export function queuedActivityRuns(activity: QueueActivity): QueuedActivityRun[] {
    const runs = activity.recordings.flatMap((entry) => {
        const recording = { recording_id: entry.recording_id, recording_title: entry.recording_title };
        const owners = occupiedWorkerRecordings(activity, entry.recording_id);
        const headExists = entry.has_worker || entry.state === 'waiting_worker' || entry.state === 'checking';
        const waiting: QueuedActivityRun[] = !entry.has_worker && entry.state === 'waiting_worker' ? [{
            ...recording,
            queue_item_id: entry.queue_item_id ?? `${entry.recording_id}:waiting`,
            queued_at: entry.queued_at,
            state: 'waiting_worker',
            model_family: entry.model_family,
            model: entry.model,
            profile_name: entry.profile_name,
            execution_id: entry.execution_id,
            run_number: entry.run_number,
            waiting_on: owners,
            wait_reason: 'worker' as const,
        }] : [];
        return [...waiting, ...(entry.queued_jobs ?? []).map((job, index) => ({
            ...recording, ...job, state: 'queued' as const,
            waiting_on: entry.has_worker
                ? [{ ...recording, execution_id: entry.execution_id, run_number: entry.run_number }]
                : owners.length ? owners : headExists || index > 0 ? [recording] : [],
            wait_reason: entry.has_worker || owners.length ? 'worker' as const : 'earlier_run' as const,
        }))];
    });
    const timestamp = (run: QueuedActivityRun) => {
        const value = Date.parse(run.queued_at ?? '');
        return Number.isFinite(value) ? value : 0;
    };
    return runs.sort((left, right) => timestamp(right) - timestamp(left));
}

export function activityStageLabel(stage?: string) {
    return ({ recognize: 'Transcription', recognition: 'Transcription', combined: 'Transcription', align: 'Word timing', alignment: 'Word timing', diarize: 'Speakers', diarization: 'Speakers', speaker_assignment: 'Speakers', assemble: 'Merging tracks', merge: 'Merging tracks' } as Record<string, string>)[stage ?? ''] ?? stage;
}

export function activityStageProgress(entry: QueueActivityEntry) {
    const label = activityStageLabel(entry.stage);
    if (!label) return undefined;
    const prefix = entry.stage_scope === 'track' ? 'Track · ' : '';
    const number = entry.stage_number ?? 0;
    const total = entry.stage_total ?? 0;
    return `${prefix}${label}${Number.isInteger(number) && Number.isInteger(total) && number > 0 && number <= total ? ` · ${number}/${total}` : ''}`;
}

export function activityStateLabel(entry: QueueActivityEntry) {
    switch (entry.state) {
        case 'resource_wait': return 'Waiting for resources';
        case 'retry_wait': return 'Retry backoff';
        case 'waiting_worker': return 'Waiting for a worker';
        case 'queued': return 'Waiting to start';
        case 'finishing': return 'Finishing up';
        case 'checking': return 'Checking worker state';
        default: return activityStageProgress(entry) ?? 'Running';
    }
}

export function isCurrentQueueHead(row: Pick<QueueActivityEntry, 'recording_id' | 'state' | 'queue_item_id' | 'execution_id'>, recordingId: string, activeQueueItemId?: string, currentExecutionId?: string) {
    if (row.recording_id !== recordingId || row.state === 'queued') return false;
    if (activeQueueItemId) return row.queue_item_id === activeQueueItemId;
    if (row.queue_item_id) return false;
    return !!currentExecutionId && row.execution_id === currentExecutionId;
}

export function occupiedWorkerRecordings(activity: QueueActivity, recordingId: string) {
    if (activity.workers < 1 || activity.busy_workers < activity.workers) return [];
    return activity.recordings.filter((row) => row.has_worker && row.recording_id !== recordingId);
}

export function occupiedWorkerMessage(activity: QueueActivity, recordingId: string) {
    const owners = occupiedWorkerRecordings(activity, recordingId);
    if (!owners.length) return null;
    const names = owners.slice(0, 2).map((row) => `“${row.recording_title}”`).join(' and ');
    const extra = owners.length > 2 ? ` and ${owners.length - 2} more recordings` : '';
    return `${names}${extra} ${owners.length === 1 ? `is using ${activity.workers === 1 ? 'the worker' : 'a worker'}` : 'are using the workers'}.`;
}

export function recordingQueueMessage(activity: QueueActivity, recordingId: string) {
    const entry = activity.recordings.find((row) => row.recording_id === recordingId);
    if (!entry) return 'No waiting runs for this recording.';
    if (entry.state === 'resource_wait') return 'This run is waiting for resources and still occupies a worker.';
    if (entry.state === 'retry_wait') return 'Automatic recovery is waiting before retrying. This run still occupies a worker.';
    if (entry.state === 'finishing') return 'The worker is finishing cleanup before the next run can start.';
    if (entry.state === 'checking') return 'Checking this recording’s worker state.';
    if (entry.has_worker) {
        return entry.queued_runs > 0
            ? `${entry.queued_runs} ${entry.queued_runs === 1 ? 'run waits' : 'runs wait'} behind this recording’s active run.`
            : 'This recording has a worker. No additional runs are queued.';
    }
    const occupied = occupiedWorkerMessage(activity, recordingId);
    return occupied
        ? `Queued for “${entry.recording_title}”. ${occupied}`
        : `Queued for “${entry.recording_title}”. Waiting for an available worker.`;
}

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { activityStateLabel, activityStageProgress, isCurrentQueueHead, occupiedWorkerMessage, queuedActivityRuns, recordingQueueMessage, type QueueActivity, type QueueActivityEntry } from './queueActivity.ts';

const entry = (id: string, state: QueueActivityEntry['state'], hasWorker: boolean, queuedRuns = 0): QueueActivityEntry => ({ recording_id: id, recording_title: id, state, has_worker: hasWorker, model_family: 'nvidia', model: 'parakeet', queued_runs: queuedRuns });
const activity: QueueActivity = { generated_at: '2026-10-01T12:00:00Z', workers: 2, busy_workers: 2, waiting_recordings: 1, queued_runs: 3, recordings: [entry('Interview', 'running', true, 3), entry('Memo', 'resource_wait', true), entry('Planning', 'waiting_worker', false)] };

test('waiting recordings name the shared worker owners without inventing positions', () => {
    assert.equal(recordingQueueMessage(activity, 'Planning'), 'Queued for “Planning”. “Interview” and “Memo” are using the workers.');
    assert.equal(recordingQueueMessage({ ...activity, busy_workers: 1 }, 'Planning'), 'Queued for “Planning”. Waiting for an available worker.');
    assert.equal(occupiedWorkerMessage({ ...activity, workers: 1, busy_workers: 1, recordings: [activity.recordings[0], activity.recordings[2]] }, 'Planning'), '“Interview” is using the worker.');
    assert.equal(occupiedWorkerMessage({ ...activity, busy_workers: 1 }, 'Planning'), null);
});
test('resource and retry waits retain workers while future runs wait within a recording', () => {
    assert.match(recordingQueueMessage(activity, 'Interview'), /3 runs wait behind this recording’s active run/);
    assert.match(recordingQueueMessage(activity, 'Memo'), /waiting for resources and still occupies a worker/);
    const retry = { ...activity, recordings: [entry('Memo', 'retry_wait', true)] };
    assert.match(recordingQueueMessage(retry, 'Memo'), /waiting before retrying/);
    assert.equal(activityStateLabel(retry.recordings[0]), 'Retry backoff');
});
test('idle and cleanup messages do not claim global idleness', () => {
    assert.equal(recordingQueueMessage(activity, 'Idle'), 'No waiting runs for this recording.');
    assert.match(recordingQueueMessage({ ...activity, recordings: [entry('Interview', 'finishing', true)] }, 'Interview'), /finishing cleanup/);
    assert.equal(activityStateLabel({ ...entry('Interview', 'running', true), stage: 'alignment' }), 'Word timing');
});

test('latest queued runs combine recording heads and future requests by admission time', () => {
    const data: QueueActivity = { ...activity, recordings: [
        { ...entry('Interview', 'running', true, 2), queue_item_id: 'active', queued_jobs: [
            { queue_item_id: 'earlier', model_family: 'nvidia', model: 'parakeet', queued_at: '2026-10-01T12:01:00Z' },
            { queue_item_id: 'latest', model_family: 'cohere', model: 'transcribe', queued_at: '2026-10-01T12:05:00Z' },
        ] },
        { ...entry('Memo', 'waiting_worker', false), queue_item_id: 'pending', queued_at: '2026-10-01T12:03:00Z', queued_jobs: [] },
        { ...entry('Planning', 'queued', false, 1), queue_item_id: 'future', queued_jobs: [
            { queue_item_id: 'future', model_family: 'nvidia', model: 'canary', queued_at: '2026-10-01T12:04:00Z' },
        ] },
    ] };
    const before = JSON.stringify(data);
    const rows = queuedActivityRuns(data);
    assert.deepEqual(rows.map((row) => row.queue_item_id), ['latest', 'future', 'pending', 'earlier']);
    assert.deepEqual(rows.slice(0, 3).map((row) => row.recording_title), ['Interview', 'Planning', 'Memo']);
    assert.equal(rows[2].state, 'waiting_worker');
    assert.equal(new Set(rows.map((row) => row.queue_item_id)).size, 4);
    assert.equal(JSON.stringify(data), before, 'display sorting must not change request order');
});

test('unknown queued times follow known admissions without inventing a date or queue position', () => {
    const rows = queuedActivityRuns({ ...activity, recordings: [
        { ...entry('Legacy', 'waiting_worker', false), queued_jobs: [] },
        { ...entry('Future', 'queued', false, 2), queued_jobs: [
            { queue_item_id: 'unknown', model_family: 'nvidia', model: 'parakeet', queued_at: 'invalid' },
            { queue_item_id: 'known', model_family: 'nvidia', model: 'canary', queued_at: '2026-10-01T12:01:00Z' },
        ] },
    ] });
    assert.deepEqual(rows.map((row) => row.queue_item_id), ['known', 'Legacy:waiting', 'unknown']);
    assert.equal(rows[1].queued_at, undefined);
});

test('stage numbering uses recorded boundaries and preserves wait states', () => {
    const alignment = { ...entry('Interview', 'running', true), stage: 'alignment', stage_number: 2, stage_total: 3 };
    assert.equal(activityStateLabel(alignment), 'Word timing · 2/3');
    assert.equal(activityStageProgress({ ...alignment, stage_scope: 'track', stage_total: 2 }), 'Track · Word timing · 2/2');
    assert.equal(activityStateLabel({ ...alignment, state: 'retry_wait' }), 'Retry backoff');
    assert.equal(activityStateLabel({ ...alignment, stage_number: 4 }), 'Word timing');
    assert.equal(activityStateLabel({ ...alignment, stage_number: undefined, stage_total: undefined }), 'Word timing');
    assert.equal(activityStateLabel({ ...alignment, stage: 'diarize', stage_number: 3 }), 'Speakers · 3/3');
    assert.equal(activityStateLabel({ ...alignment, stage: 'speaker_assignment', stage_number: 3 }), 'Speakers · 3/3');
});

test('waiting targets distinguish worker owners from earlier runs within a recording', () => {
    const data = { ...activity, recordings: [
        { ...entry('Interview', 'running', true), execution_id: 'active', run_number: 4, queued_jobs: [{ queue_item_id: 'next', model_family: 'cohere', model: 'transcribe' }] },
        entry('Memo', 'retry_wait', true),
        { ...entry('Planning', 'waiting_worker', false), queued_jobs: [{ queue_item_id: 'future', model_family: 'nvidia', model: 'parakeet' }] },
    ] };
    const rows = queuedActivityRuns(data);
    assert.deepEqual(rows.find((row) => row.queue_item_id === 'Planning:waiting')?.waiting_on?.map((row) => row.recording_id), ['Interview', 'Memo']);
    assert.deepEqual(rows.find((row) => row.queue_item_id === 'future')?.waiting_on?.map((row) => row.recording_id), ['Interview', 'Memo']);
    assert.equal(rows.find((row) => row.queue_item_id === 'next')?.waiting_on?.[0].execution_id, 'active');
    assert.equal(rows.find((row) => row.queue_item_id === 'next')?.waiting_on?.[0].run_number, 4);
    assert.deepEqual(queuedActivityRuns({ ...data, busy_workers: 1 }).find((row) => row.queue_item_id === 'Planning:waiting')?.waiting_on, []);
    const future = queuedActivityRuns({ ...data, busy_workers: 1 }).find((row) => row.queue_item_id === 'future');
    assert.deepEqual(future?.waiting_on?.map((row) => row.recording_id), ['Planning']);
    assert.equal(future?.wait_reason, 'earlier_run');
});

test('stop controls require the exact current recording and request identity', () => {
    const row = { ...entry('Interview', 'running', true), queue_item_id: 'old', execution_id: 'run-old' };
    assert.equal(isCurrentQueueHead(row, 'Interview', 'new', 'run-new'), false);
    assert.equal(isCurrentQueueHead(row, 'Interview', 'old', 'run-old'), true);
    assert.equal(isCurrentQueueHead(row, 'Other', 'old', 'run-old'), false);
    assert.equal(isCurrentQueueHead({ ...row, queue_item_id: undefined }, 'Interview', 'new', 'run-new'), false);
    assert.equal(isCurrentQueueHead({ ...row, queue_item_id: undefined }, 'Interview', undefined, 'run-new'), false);
    assert.equal(isCurrentQueueHead({ ...row, queue_item_id: undefined }, 'Interview', undefined, 'run-old'), true);
    assert.equal(isCurrentQueueHead({ ...row, state: 'queued' }, 'Interview', 'old', 'run-old'), false);
    assert.equal(isCurrentQueueHead(row, 'Interview'), false);
});

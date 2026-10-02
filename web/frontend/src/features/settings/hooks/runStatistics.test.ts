import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
	activityDays,
	statisticsMemory,
	statisticsDuration,
	statisticsDevice
} from './runStatistics.ts';

test('missing telemetry stays distinct from zero measurements', () => {
	assert.equal(statisticsMemory(null), '—');
	assert.equal(statisticsMemory(0), '0.0 GiB');
	assert.equal(statisticsMemory(1024 ** 3), '1.0 GiB');
	assert.equal(statisticsDuration(undefined), '—');
	assert.equal(statisticsDuration(0), '0s');
	assert.equal(statisticsDuration(3600), '1h 0m');
	assert.equal(statisticsDevice('unknown'), 'Not recorded');
});

test('activity fills missing days in UTC without fabricating runs', () => {
	const rows = activityDays(
		[{ date: '2026-09-30', completed: 2, failed: 1, other: 0 }],
		'2026-10-01T00:00:00Z',
		3
	);
	assert.deepEqual(rows, [
		{ date: '2026-09-29', completed: 0, failed: 0, other: 0 },
		{ date: '2026-09-30', completed: 2, failed: 1, other: 0 },
		{ date: '2026-10-01', completed: 0, failed: 0, other: 0 }
	]);
	assert.deepEqual(activityDays([], 'invalid'), []);
});

import assert from 'node:assert/strict';
import test from 'node:test';
import { attemptResourceValues, resourceBytes, summarizeRunResources } from './runResources.ts';
import type { ResourceMeasurements } from './recoveryPolicy.ts';

const measurement = (fields: Partial<ResourceMeasurements>): ResourceMeasurements => ({
	samples: 2,
	elapsed_seconds: 10,
	scope: 'fixture',
	external_contention: false,
	...fields
});

test("run averages use measured seconds, peaks use maxima, and GPU reserve uses each invocation's capacity", () => {
	const summary = summarizeRunResources([
		measurement({
			elapsed_seconds: 10,
			process_peak_rss_bytes: 900,
			process_average_rss_bytes: 300,
			rss_sampled_seconds: 10,
			process_cpu_average_percent: 200,
			cpu_sampled_seconds: 5,
			device_peak_used_bytes: 600,
			gpu_total_bytes: 1000
		}),
		measurement({
			elapsed_seconds: 30,
			process_peak_rss_bytes: 600,
			process_average_rss_bytes: 500,
			rss_sampled_seconds: 30,
			process_cpu_average_percent: 600,
			cpu_sampled_seconds: 15,
			device_peak_used_bytes: 700,
			gpu_total_bytes: 1200
		})
	]);
	assert.equal(summary.peakRSS, 900);
	assert.equal(summary.averageRSS.value, 450);
	assert.equal(summary.averageRSS.seconds, 40);
	assert.equal(summary.averageCPU.value, 500);
	assert.equal(summary.minimumGPUHeadroom, 400);
	assert.equal(summary.minimumGPUHeadroomPercent, 40);
	assert.equal(summary.peakDeviceVRAM, 700);
});

test('legacy and missing averages stay unknown, while a measured zero remains valid', () => {
	const old = measurement({ process_peak_bytes: 512 });
	assert.equal(summarizeRunResources([old]).peakRSS, undefined);
	assert.equal(summarizeRunResources([old]).averageRSS.value, undefined);
	assert.equal(
		attemptResourceValues({
			id: 'gpu',
			status: 'succeeded',
			attempt_number: 1,
			device: 'cuda',
			measurements: old
		}).peakRSS,
		undefined
	);
	assert.equal(
		attemptResourceValues({
			id: 'cpu',
			status: 'succeeded',
			attempt_number: 1,
			device: 'cpu',
			measurements: old
		}).peakRSS,
		512
	);
	assert.equal(
		summarizeRunResources([measurement({ process_cpu_average_percent: 0, cpu_sampled_seconds: 5 })])
			.averageCPU.value,
		0
	);
	assert.equal(resourceBytes(undefined), 'Not recorded');
	assert.equal(resourceBytes(0), '0.0 MiB');
});

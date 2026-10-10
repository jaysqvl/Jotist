import test from 'node:test';
import assert from 'node:assert/strict';
import { setTimeout as wait } from 'node:timers/promises';
import { modelStorageEventParser, watchModelStorage } from './modelStorageEvents.ts';

const encoded = (value: string) => new TextEncoder().encode(value);
const frame = (type: string) => `data: ${JSON.stringify({ type })}\n\n`;

test('storage event parsing handles split frames, CRLF, keepalives and malformed messages', () => {
    const events: string[] = [];
    const parse = modelStorageEventParser(type => events.push(type));
    parse(': keepalive\r\n\r\ndata: {"ty');
    parse('pe":"connected"}\r');
    parse('\n\r\ndata: not-json\n\ndata: {"type":"model_storage_changed"}\n');
    parse('\ndata: {"other":"ignored"}\n\n');
    assert.deepEqual(events, ['connected', 'model_storage_changed']);
    assert.throws(() => parse('x'.repeat(65537)), /Oversized/);
});

test('storage updates coalesce events and stay idle without inventory polling', async () => {
    let stream!: ReadableStreamDefaultController<Uint8Array>;
    let requests = 0;
    let changes = 0;
    let requestSignal: AbortSignal | null | undefined;
    const connections: boolean[] = [];
    const fetcher: typeof fetch = async (url, init) => {
        requests++;
        assert.equal(url, '/api/v1/settings/models/events');
        assert.deepEqual(init?.headers, { Authorization: 'Bearer disposable-token' });
        requestSignal = init?.signal;
        return new Response(new ReadableStream<Uint8Array>({ start(controller) {
            stream = controller;
            init?.signal?.addEventListener('abort', () => controller.error(new DOMException('Aborted', 'AbortError')), { once: true });
        } }));
    };
    const stop = watchModelStorage('disposable-token', () => changes++, value => connections.push(value), fetcher);
    try {
        await wait(0);
        stream.enqueue(encoded(frame('connected') + frame('model_storage_changed') + frame('model_storage_changed')));
        await wait(250);
        assert.equal(changes, 1);
        assert.deepEqual(connections, [true]);
        stream.enqueue(encoded(': keepalive\n\n'));
        await wait(250);
        assert.equal(requests, 1, 'an idle page must keep a single event connection');
        assert.equal(changes, 1, 'keepalives must not query inventory');
        stream.enqueue(encoded(frame('model_storage_changed')));
        stop();
        await wait(250);
        assert.equal(changes, 1, 'unmount must cancel a pending refresh');
        assert.equal(requestSignal?.aborted, true);
    } finally { stop(); }
});

test('storage events reconnect on clean EOF and reconcile missed changes', async () => {
    let requests = 0;
    let changes = 0;
    const connections: boolean[] = [];
    const fetcher: typeof fetch = async (_url, init) => {
        requests++;
        const first = requests === 1;
        return new Response(new ReadableStream<Uint8Array>({ start(controller) {
            controller.enqueue(encoded(frame('connected')));
            if (first) controller.close();
            else init?.signal?.addEventListener('abort', () => controller.error(new DOMException('Aborted', 'AbortError')), { once: true });
        } }));
    };
    const stop = watchModelStorage('token', () => changes++, value => connections.push(value), fetcher);
    try {
        await wait(1300);
        assert.equal(requests, 2);
        assert.deepEqual(connections, [true, false, true]);
        assert.equal(changes, 2, 'connection and reconnect both reconcile inventory');
        stop();
        await wait(1100);
        assert.equal(requests, 2, 'aborted subscriptions must not reconnect');
    } finally { stop(); }
});

test('storage events do not keep retrying a rejected token', async () => {
    let requests = 0;
    const connections: boolean[] = [];
    const fetcher: typeof fetch = async () => { requests++; return new Response(null, { status: 401 }); };
    const stop = watchModelStorage('expired-token', () => assert.fail('unauthorized events'), value => connections.push(value), fetcher);
    try {
        await wait(1100);
        assert.equal(requests, 1);
        assert.deepEqual(connections, [false]);
    } finally { stop(); }
});

test('a silent event connection reconnects without an inventory timer', async () => {
    let requests = 0;
    let changes = 0;
    const connections: boolean[] = [];
    const fetcher: typeof fetch = async (_url, init) => {
        requests++;
        return new Response(new ReadableStream<Uint8Array>({ start(controller) {
            controller.enqueue(encoded(frame('connected')));
            init?.signal?.addEventListener('abort', () => controller.error(new DOMException('Aborted', 'AbortError')), { once: true });
        } }));
    };
    const stop = watchModelStorage('token', () => changes++, value => connections.push(value), fetcher, 300);
    try {
        await wait(250);
        assert.equal(requests, 1);
        assert.equal(changes, 1);
        await wait(1200);
        assert.equal(requests, 2, 'only a silent stream should be reconnected');
        assert.deepEqual(connections, [true, false, true]);
        await wait(100);
        assert.equal(changes, 2, 'only connection events should reconcile inventory');
    } finally { stop(); }
});

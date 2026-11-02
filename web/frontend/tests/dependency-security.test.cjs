const assert = require('node:assert/strict');
const { mkdtempSync, mkdirSync, writeFileSync, rmSync } = require('node:fs');
const fs = require('node:fs');
const { tmpdir } = require('node:os');
const { join } = require('node:path');
const { createRequire } = require('node:module');
const { test } = require('node:test');

// Resolve through the consuming dependency, so an unused patched copy cannot
// make the security regression pass while the build still loads another copy.
const rollupRequire = createRequire(require.resolve('@rollup/plugin-node-resolve'));
const merge = rollupRequire('deepmerge');

test('the linter matcher bounds globstar preprocessing and retains ordinary matching', () => {
  const eslintRequire = createRequire(require.resolve('eslint'));
  const { Minimatch, minimatch } = eslintRequire('minimatch');
  const pattern = Array(30).fill('**/../a/b').join('/');
  const bounded = new Minimatch(pattern, { optimizationLevel: 2, braceExpandMax: 64 });
  assert.equal(bounded.set.length, 64);
  assert.equal(minimatch('src/settings/Models.tsx', '**/*.{ts,tsx}'), true);
  assert.equal(minimatch('src/settings/Models.tsx', '**/*.js'), false);
});

test('deepmerge cannot change a result prototype through source or target keys', () => {
  const malicious = () => JSON.parse('{"__proto__":{"admin":true}}');
  const cases = [
    [Object.create(null), malicious()],
    [{}, malicious()],
    [malicious(), {}],
    [{ session: null }, { session: malicious() }],
    [{ session: 0 }, { session: malicious() }],
    [{ session: false }, { session: malicious() }],
    [{ session: undefined }, { session: malicious() }],
    [{ list: [] }, { list: [malicious()] }],
  ];
  for (const [target, source] of cases) {
    const result = merge(target, source);
    for (const object of [result, result.session, ...(result.list || [])]) {
      if (object && typeof object === 'object') {
        assert.equal(object.admin, undefined);
        assert.equal(Object.hasOwn(object, '__proto__'), false);
        assert.equal(Object.getPrototypeOf(object), Object.prototype);
      }
    }
  }
  assert.equal(({}).admin, undefined);
});

test('deepmerge preserves ordinary nested merges, array options, symbols and cloning', () => {
  const symbol = Symbol('metadata');
  const left = { list: [1], nested: { x: 1 }, [symbol]: { a: 1 } };
  const right = { list: [2], nested: { y: 2 }, [symbol]: { b: 2 } };
  assert.deepEqual(merge(left, right), {
    list: [1, 2], nested: { x: 1, y: 2 }, [symbol]: { a: 1, b: 2 },
  });
  assert.deepEqual(merge(left, right, { arrayMerge: (_a, b) => b }).list, [2]);
  const custom = merge({ nested: { x: 1 } }, { nested: { y: 2 } }, {
    customMerge: key => key === 'nested' ? (a, b) => ({ ...a, ...b, custom: true }) : undefined,
  });
  assert.deepEqual(custom.nested, { x: 1, y: 2, custom: true });
  assert.deepEqual(merge.all([{ a: 1 }, { b: 2 }]), { a: 1, b: 2 });
  const cloned = merge({}, left);
  assert.notEqual(cloned.list, left.list);
  assert.notEqual(cloned.nested, left.nested);
  assert.equal(merge({}, left, { clone: false }).nested, left.nested);
  const date = new Date(0);
  assert.equal(merge({}, { date }).date, date);
});

test('Workbox glob stops scheduling directory reads after cancellation', async () => {
  const workboxRequire = createRequire(require.resolve('workbox-build'));
  const { glob } = workboxRequire('glob');
  const directory = mkdtempSync(join(tmpdir(), 'jotist-glob-test-'));
  try {
    for (let i = 0; i < 8; i++) {
      mkdirSync(join(directory, String(i), 'nested'), { recursive: true });
      writeFileSync(join(directory, String(i), 'nested', 'file.txt'), 'test');
    }
    assert.equal((await glob('**/*.txt', { cwd: directory })).length, 8);
    const jakeRequire = createRequire(require.resolve('jake'));
    const { FileList } = jakeRequire('filelist');
    const files = new FileList();
    files.include(join(directory, '**/*.txt'));
    files.exclude(join(directory, '0/**/*.txt'));
    assert.equal(files.toArray().length, 7);
    const controller = new AbortController();
    let reads = 0;
    const trackedFS = { ...fs, readdir(...args) {
      reads++;
      if (reads === 1) controller.abort(new Error('probe-abort'));
      return fs.readdir(...args);
    } };
    await assert.rejects(glob('**/*', { cwd: directory, signal: controller.signal, fs: trackedFS }), /probe-abort/);
    const readsAtRejection = reads;
    await new Promise(resolve => setTimeout(resolve, 40));
    assert.equal(reads, readsAtRejection);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test('the serializer escapes closing script tags at function boundaries', () => {
  const terserRequire = createRequire(require.resolve('@rollup/plugin-terser'));
  const serialize = terserRequire('serialize-javascript');
  function f() {}
  function g() {}
  f.toString = () => 'function f(){ return 1 }//</script';
  g.toString = () => 'function g(x){ return x } > <img src=x onerror=alert(1)>';
  assert.doesNotMatch(serialize({ a: { f }, g }, { space: 2 }), /<\/script/i);
  assert.equal(serialize({ ordinary: 'text' }), '{"ordinary":"text"}');
});

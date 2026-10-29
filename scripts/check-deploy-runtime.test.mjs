import assert from 'node:assert/strict';
import test from 'node:test';
import { supportsRuntime } from './check-deploy-runtime.mjs';

test('rejects the Node 20 runtime that broke the pinned Wrangler deployment', () => {
  assert.equal(supportsRuntime('20.20.2', '>=22.0.0'), false);
});
test('accepts the configured Node 22 minimum and later patch versions', () => {
  assert.equal(supportsRuntime('22.0.0', '>=22.0.0'), true);
  assert.equal(supportsRuntime('22.1.0', '>=22.0.0'), true);
  assert.equal(supportsRuntime('26.4.0', '>=22.0.0'), true);
});
test('compares minor and patch versions numerically', () => {
  assert.equal(supportsRuntime('22.10.0', '>=22.9.0'), true);
  assert.equal(supportsRuntime('22.9.2', '>=22.9.3'), false);
});
test('fails closed on missing, unexpected, and executable-looking metadata', () => {
  for (const requirement of ['', '*', '>=22 || >=24', '$(touch unsafe)', '>=22.0.0;echo unsafe']) {
    assert.throws(() => supportsRuntime('22.0.0', requirement));
  }
  assert.throws(() => supportsRuntime('unknown', '>=22.0.0'));
});

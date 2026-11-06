import test from 'node:test';
import assert from 'node:assert/strict';
import { handleContact, newTicketId, parseSubmission } from './contact.js';

const INBOX = 'owner-inbox@example.test';
const good = { name: 'Ada', email: 'ada@acme.test', company: 'Acme', tier: 'pro', message: 'Hello <b>x</b>' };

function setup(overrides = {}) {
  const sent = [];
  const env = { TICKET_INBOX: INBOX, EMAIL: { send: async (m) => { sent.push(m); } }, ...overrides };
  const ctx = { waitUntil: (p) => p };
  return { sent, env, ctx };
}
const post = (body, headers = {}) => new Request('https://joltrinhq.com/api/contact', {
  method: 'POST', headers: { 'Content-Type': 'application/json', Origin: 'https://joltrinhq.com', ...headers }, body: JSON.stringify(body),
});

test('ticket id has the expected shape', () => {
  assert.match(newTicketId(new Date('2026-11-01T00:00:00Z')), /^JTR-20261101-[A-HJKMNP-Z2-9]{6}$/);
});

test('valid submission emails the inbox, sends a receipt, and returns the ticket', async () => {
  const { sent, env, ctx } = setup();
  const res = await handleContact(post(good), env, ctx);
  assert.equal(res.status, 200);
  const { ticket, receipt } = await res.json();
  assert.equal(receipt, true);
  assert.match(ticket, /^JTR-\d{8}-[A-Z2-9]{6}$/);
  assert.equal(sent.length, 2);
  assert.equal(sent[0].to, INBOX);
  assert.equal(sent[0].replyTo.email, 'ada@acme.test');
  assert.ok(sent[0].subject.includes(ticket));
  assert.ok(sent[0].html.includes('&lt;b&gt;x&lt;/b&gt;'), 'html is escaped');
  assert.equal(sent[1].to, 'ada@acme.test');
  assert.ok(sent[1].text.includes(ticket));
});

test('the response never contains the inbox address', async () => {
  const { env, ctx } = setup();
  const res = await handleContact(post(good), env, ctx);
  assert.ok(!(await res.text()).includes(INBOX));
});

test('missing or bad fields are rejected', async () => {
  const { sent, env, ctx } = setup();
  for (const bad of [{ ...good, name: '' }, { ...good, email: 'nope' }, { ...good, company: ' ' }]) {
    assert.equal((await handleContact(post(bad), env, ctx)).status, 400);
  }
  assert.equal(sent.length, 0);
});

test('honeypot looks like success but sends nothing', async () => {
  const { sent, env, ctx } = setup();
  const res = await handleContact(post({ ...good, hp_check: 'x' }), env, ctx);
  assert.equal(res.status, 200);
  assert.equal(sent.length, 0);
});

test('foreign origin, wrong method, wrong type, oversize and bad json are refused', async () => {
  const { env, ctx } = setup();
  assert.equal((await handleContact(post(good, { Origin: 'https://evil.test' }), env, ctx)).status, 403);
  assert.equal((await handleContact(new Request('https://joltrinhq.com/api/contact'), env, ctx)).status, 405);
  assert.equal((await handleContact(post(good, { 'Content-Type': 'text/plain' }), env, ctx)).status, 415);
  assert.equal((await handleContact(post({ ...good, pad: 'x'.repeat(20000) }), env, ctx)).status, 413);
  const bad = new Request('https://joltrinhq.com/api/contact', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{' });
  assert.equal((await handleContact(bad, env, ctx)).status, 400);
});

test('rate limit returns 429', async () => {
  const { env, ctx } = setup({ CONTACT_LIMIT: { limit: async () => ({ success: false }) } });
  assert.equal((await handleContact(post(good), env, ctx)).status, 429);
});

test('unconfigured inbox returns 503 and no ticket', async () => {
  const { env, ctx } = setup({ TICKET_INBOX: undefined });
  const res = await handleContact(post(good), env, ctx);
  assert.equal(res.status, 503);
  assert.equal((await res.json()).ticket, undefined);
});

test('inbox delivery failure returns 502 with no ticket; receipt failure still succeeds', async () => {
  const failing = setup({ EMAIL: { send: async () => { throw new Error('boom'); } } });
  const r1 = await handleContact(post(good), failing.env, failing.ctx);
  assert.equal(r1.status, 502);
  assert.equal((await r1.json()).ticket, undefined);

  let n = 0;
  const flaky = setup({ EMAIL: { send: async () => { if (n++ === 1) throw new Error('receipt down'); } } });
  const r2 = await handleContact(post(good), flaky.env, flaky.ctx);
  assert.equal(r2.status, 200);
  const body = await r2.json();
  assert.match(body.ticket, /^JTR-/);
  assert.equal(body.receipt, false);
});

test('control characters and header-injection newlines are stripped from one-line fields', () => {
  const { fields } = parseSubmission({ ...good, name: 'Ada\r\nBcc: x@y.test', company: 'Acme' });
  assert.ok(!/[\r\n]/.test(fields.name));
});

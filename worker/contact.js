// Contact form handler for joltrinhq.com. Turns a submission into a ticket:
// it makes a ticket ID, emails the team inbox, and emails the sender a receipt
// with the same ID. The inbox address is a Worker secret (TICKET_INBOX), so it
// never appears in the site, the repo or the responses.

const ID_ALPHABET = 'ABCDEFGHJKMNPQRSTUVWXYZ23456789'; // no 0/O, 1/I/L
const MAX_BODY = 16 * 1024;
const LIMITS = { name: 120, email: 254, company: 160, role: 120, website: 300, message: 4000, short: 200 };
const EMAIL_RE = /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]{2,}$/;
const ALLOWED_ORIGINS = ['https://joltrinhq.com', 'https://www.joltrinhq.com'];

export function newTicketId(now = new Date(), rand = crypto) {
  const bytes = new Uint8Array(6);
  rand.getRandomValues(bytes);
  const suffix = Array.from(bytes, (b) => ID_ALPHABET[b % ID_ALPHABET.length]).join('');
  const day = now.toISOString().slice(0, 10).replaceAll('-', '');
  return `JTR-${day}-${suffix}`;
}

const clean = (v, max) => String(v ?? '').replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/g, '').trim().slice(0, max);
const oneLine = (v, max) => clean(v, max).replace(/\s+/g, ' ');
const esc = (s) => s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

function json(body, status, extra = {}) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', ...extra },
  });
}

export function parseSubmission(raw) {
  const f = {
    name: oneLine(raw.name, LIMITS.name),
    email: oneLine(raw.email, LIMITS.email),
    company: oneLine(raw.company, LIMITS.company),
    role: oneLine(raw.role, LIMITS.role),
    website: oneLine(raw.website, LIMITS.website),
    tier: oneLine(raw.tier, 20),
    company_size: oneLine(raw.company_size, LIMITS.short),
    use_cases: oneLine(raw.use_cases, LIMITS.short),
    approx_agents: oneLine(raw.approx_agents, LIMITS.short),
    idp: oneLine(raw.idp, LIMITS.short),
    deployment: oneLine(raw.deployment, LIMITS.short),
    message: clean(raw.message, LIMITS.message),
    source: oneLine(raw.source, 60),
  };
  if (!f.name) return { error: 'Please enter your name.' };
  if (!EMAIL_RE.test(f.email)) return { error: 'Please enter a valid email address.' };
  if (!f.company) return { error: 'Please enter your company.' };
  return { fields: f };
}

function ownerMessage(id, f, meta) {
  const rows = [
    ['Name', f.name], ['Email', f.email], ['Company', f.company], ['Role', f.role],
    ['Plan', f.tier], ['Company size', f.company_size], ['Use case', f.use_cases],
    ['Agents', f.approx_agents], ['Identity provider', f.idp], ['Deployment', f.deployment],
    ['Website', f.website], ['Submitted from', f.source || meta.path],
  ].filter(([, v]) => v);
  const text = [`Ticket ${id}`, '', ...rows.map(([k, v]) => `${k}: ${v}`), '', 'Message:', f.message || '(none)', '',
    `Received ${meta.at} | country ${meta.country || 'unknown'}`, `Reply to this email to answer ${f.name}.`].join('\n');
  const html = `<h2 style="margin:0 0 12px">Ticket ${esc(id)}</h2><table cellpadding="4" style="border-collapse:collapse">${rows
    .map(([k, v]) => `<tr><td style="color:#666">${esc(k)}</td><td>${esc(v)}</td></tr>`).join('')}</table><p><b>Message</b></p><pre style="white-space:pre-wrap;font:inherit">${esc(f.message || '(none)')}</pre><p style="color:#666">Received ${esc(meta.at)}. Reply to this email to answer ${esc(f.name)}.</p>`;
  return { subject: `[${id}] ${f.company}: ${(f.tier || 'inquiry').toUpperCase()} inquiry from ${f.name}`, text, html };
}

function receiptMessage(id, f) {
  const text = `Hi ${f.name},\n\nThanks for contacting Joltrin. We got your message and opened a ticket.\n\nTicket ID: ${id}\n\nA person reads every ticket and will reply to this address. If you write to us again, keep the ticket ID in the subject so we can find it.\n\nThanks,\nJoltrin\n`;
  const html = `<p>Hi ${esc(f.name)},</p><p>Thanks for contacting Joltrin. We got your message and opened a ticket.</p><p><b>Ticket ID: ${esc(id)}</b></p><p>A person reads every ticket and will reply to this address. If you write to us again, keep the ticket ID in the subject so we can find it.</p><p>Thanks,<br>Joltrin</p>`;
  return { subject: `We received your message [${id}]`, text, html };
}

// Returns the body as text, or null if it is larger than MAX_BODY bytes. A
// client that declares a large Content-Length is refused before any of it is
// read, and one that lies or sends no length is cut off at the limit.
async function readBody(request) {
  if (Number(request.headers.get('Content-Length')) > MAX_BODY) return null;
  if (!request.body) return '';
  const reader = request.body.getReader();
  const chunks = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > MAX_BODY) {
      await reader.cancel();
      return null;
    }
    chunks.push(value);
  }
  const all = new Uint8Array(size);
  let at = 0;
  for (const c of chunks) { all.set(c, at); at += c.byteLength; }
  return new TextDecoder().decode(all);
}

export async function handleContact(request, env) {
  const origin = request.headers.get('Origin');
  const originOk = !origin || ALLOWED_ORIGINS.includes(origin) || origin === new URL(request.url).origin;
  const cors = origin && originOk ? { 'Access-Control-Allow-Origin': origin, Vary: 'Origin' } : {};

  if (request.method === 'OPTIONS') {
    return new Response(null, { status: 204, headers: { ...cors, 'Access-Control-Allow-Methods': 'POST', 'Access-Control-Allow-Headers': 'Content-Type', 'Access-Control-Max-Age': '86400' } });
  }
  if (request.method !== 'POST') return json({ error: 'Method not allowed' }, 405, { Allow: 'POST, OPTIONS' });
  if (!originOk) return json({ error: 'Origin not allowed' }, 403);
  if (!(request.headers.get('Content-Type') || '').includes('application/json')) return json({ error: 'Send JSON.' }, 415, cors);

  const ip = request.headers.get('CF-Connecting-IP') || 'unknown';
  if (env.CONTACT_LIMIT) {
    const { success } = await env.CONTACT_LIMIT.limit({ key: ip });
    if (!success) return json({ error: 'Too many requests. Please try again in a minute.' }, 429, { ...cors, 'Retry-After': '60' });
  }

  // Measure bytes, not characters, and stop reading once the body is over the limit.
  const text = await readBody(request);
  if (text === null) return json({ error: 'That message is too large.' }, 413, cors);
  let raw;
  try { raw = JSON.parse(text); } catch { return json({ error: 'Invalid request.' }, 400, cors); }
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return json({ error: 'Invalid request.' }, 400, cors);

  // Hidden field a person never fills in. Pretend it worked so bots do not retry.
  if (raw.hp_check) return json({ status: 'ok', ticket: newTicketId() }, 200, cors);

  const parsed = parseSubmission(raw);
  if (parsed.error) return json({ error: parsed.error }, 400, cors);
  const f = parsed.fields;

  if (!env.TICKET_INBOX || !env.EMAIL) {
    console.error('contact: TICKET_INBOX secret or EMAIL binding is not configured');
    return json({ error: 'The contact form is not available right now. Please try again later.' }, 503, cors);
  }

  const id = newTicketId();
  const from = { email: env.TICKET_FROM || 'support@joltrinhq.com', name: 'Joltrin Support' };
  const meta = { at: new Date().toISOString(), country: request.cf?.country, path: new URL(request.url).pathname };

  try {
    const m = ownerMessage(id, f, meta);
    await env.EMAIL.send({ to: env.TICKET_INBOX, from, replyTo: { email: f.email, name: f.name }, ...m });
  } catch (err) {
    console.error('contact: could not deliver ticket', id, err && err.message);
    return json({ error: 'We could not send your message. Please try again in a few minutes.' }, 502, cors);
  }

  // The receipt is a courtesy. The ticket already exists if it fails, for example
  // on a plan that cannot send to arbitrary addresses. The reply says which happened.
  let receipt = false;
  try {
    await env.EMAIL.send({ to: f.email, from, ...receiptMessage(id, f) });
    receipt = true;
  } catch (err) {
    console.error('contact: receipt failed', id, err && err.message);
  }

  return json({ status: 'ok', ticket: id, receipt }, 200, cors);
}

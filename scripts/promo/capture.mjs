// Runs the real sop-mcp-server and records what it prints, so the video shows
// real output and nothing typed in by hand.
//
//   node scripts/promo/capture.mjs <path-to-sop-mcp-server> <out.json>
import { spawn, execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';

const [bin, out] = process.argv.slice(2);
if (!bin || !out) {
  console.error('usage: node scripts/promo/capture.mjs <sop-mcp-server> <out.json>');
  process.exit(2);
}

const demoText = execFileSync(bin, ['demo'], { encoding: 'utf8' });
const demoJSON = JSON.parse(execFileSync(bin, ['demo', '--json'], { encoding: 'utf8' }));
const version = execFileSync(bin, ['version'], { encoding: 'utf8' }).trim();

// One real execute_step over MCP stdio: the call the agent makes, and the
// structured block it gets back.
const blocked = await new Promise((resolve, reject) => {
  const p = spawn(bin, [], { stdio: ['pipe', 'pipe', 'ignore'] });
  let buf = '';
  const waiting = new Map();
  p.stdout.on('data', (d) => {
    buf += d;
    let i;
    while ((i = buf.indexOf('\n')) >= 0) {
      const line = buf.slice(0, i);
      buf = buf.slice(i + 1);
      if (!line.trim()) continue;
      const m = JSON.parse(line);
      if (m.id !== undefined && waiting.has(m.id)) waiting.get(m.id)(m);
    }
  });
  const rpc = (id, method, params) =>
    new Promise((res) => {
      waiting.set(id, res);
      p.stdin.write(JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n');
    });
  (async () => {
    await rpc(1, 'initialize', { protocolVersion: '2024-11-05', capabilities: {}, clientInfo: { name: 'promo', version: '0' } });
    p.stdin.write(JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' }) + '\n');
    const call = { workflow: 'db-maintenance', trace_id: 'promo-1', step: 'drop_prod_db' };
    const r = await rpc(2, 'tools/call', { name: 'execute_step', arguments: call });
    p.kill();
    resolve({ call, result: r.result.structuredContent });
  })().catch(reject);
});

writeFileSync(out, JSON.stringify({ version, demoText, demo: demoJSON, mcp: blocked }, null, 2));
console.log('captured', version, 'to', out);

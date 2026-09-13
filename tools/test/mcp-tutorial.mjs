// Run with the installed Kado path. Controlled loopback OAuth/MCP only.
// KADO_MCP_TUTORIAL_SCRIPT enables this through TestPublicMCPCompleteBundle.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { randomUUID, createHash } from 'node:crypto';
import { mkdtemp, mkdir, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { spawn } from 'node:child_process';

const kado = resolve(process.argv[2]);
const windows = process.platform === 'win32';
const work = await mkdtemp(join(tmpdir(), 'Kado MCP tutorial ü '));
const home = join(work, 'profiles');
await mkdir(home);
const access = randomUUID(), codes = new Map(), sockets = new Set(), calls = [];
let origin;
const server = createServer(async (req, res) => {
  const json = (value, status = 200) => res.writeHead(status, { 'content-type': 'application/json' }).end(JSON.stringify(value));
  try {
    const url = new URL(req.url, origin);
    let raw = ''; for await (const part of req) raw += part;
    if (url.pathname.includes('oauth-protected-resource')) return json({ resource: `${origin}/mcp`, authorization_servers: [origin] });
    if (url.pathname.includes('.well-known/')) return json({ issuer: origin, authorization_endpoint: `${origin}/authorize`, token_endpoint: `${origin}/token`, registration_endpoint: `${origin}/register`, response_types_supported: ['code'], grant_types_supported: ['authorization_code'], code_challenge_methods_supported: ['S256'], token_endpoint_auth_methods_supported: ['none'] });
    if (url.pathname === '/register') return json({ ...JSON.parse(raw), client_id: 'tutorial-client', token_endpoint_auth_method: 'none' }, 201);
    if (url.pathname === '/token') {
      const body = new URLSearchParams(raw), request = codes.get(body.get('code'));
      assert.ok(request); codes.delete(body.get('code'));
      assert.equal(body.get('client_id'), 'tutorial-client');
      assert.equal(createHash('sha256').update(body.get('code_verifier')).digest('base64url'), request.get('code_challenge'));
      return json({ access_token: access, token_type: 'Bearer', expires_in: 3600, scope: 'read' });
    }
    if (req.headers.authorization !== `Bearer ${access}`) {
      res.setHeader('www-authenticate', `Bearer resource_metadata="${origin}/.well-known/oauth-protected-resource"`);
      return json({ error: 'unauthorized' }, 401);
    }
    if (req.method === 'GET') { res.writeHead(200, { 'content-type': 'text/event-stream' }); res.write(': ready\n\n'); return; }
    if (req.method === 'DELETE') { res.writeHead(200).end(); return; }
    const message = JSON.parse(raw);
    if (message.id === undefined) { res.writeHead(202).end(); return; }
    const reply = result => json({ jsonrpc: '2.0', id: message.id, result });
    if (message.method === 'server/discover') return json({ jsonrpc: '2.0', id: message.id, error: { code: -32601, message: 'Legacy fixture' } });
    if (message.method === 'initialize') return reply({ protocolVersion: '2025-11-25', capabilities: { tools: {} }, serverInfo: { name: 'tutorial', version: '1' } });
    if (message.method === 'ping') return reply({});
    if (message.method === 'tools/list') return reply({ tools: [{ name: 'selected_tool', description: 'Echo test-owned text', inputSchema: { type: 'object', properties: { text: { type: 'string' } }, required: ['text'], additionalProperties: false } }] });
    if (message.method === 'tools/call') {
      assert.equal(message.params.name, 'selected_tool');
      assert.equal(message.params.arguments.text, 'hello ü');
      calls.push(message.params);
      return reply({ content: [{ type: 'text', text: 'tutorial completed' }] });
    }
    return json({ jsonrpc: '2.0', id: message.id, error: { code: -32601, message: 'Unsupported fixture method' } });
  } catch { return json({ error: 'controlled fixture rejected request' }, 500); }
});
server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
origin = `http://127.0.0.1:${server.address().port}`;
const endpoint = `${origin}/mcp`;
const env = { ...process.env, KADO_MCP_HOME_DIR: home, KADO_MAINTENANCE_CHILD: '1', NO_COLOR: '1', PATH: dirname(kado) + (windows ? ';' : ':') + process.env.PATH };
// Literal documentation commands execute through the user's shell, not argv
// reconstruction. Placeholder substitution is confined to our loopback URL.
const run = (command, authorize = false) => new Promise((resolve, reject) => {
  const shell = windows ? 'powershell.exe' : '/bin/sh';
  const args = windows ? ['-NoProfile', '-NonInteractive', '-Command', command] : ['-c', command];
  const child = spawn(shell, args, { cwd: work, env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  let stdout = '', stderr = '', approved = false;
  const timer = setTimeout(() => { child.kill(); reject(Error('tutorial command timed out')); }, 25000);
  child.stdout.setEncoding('utf8').on('data', chunk => stdout += chunk);
  child.stderr.setEncoding('utf8').on('data', chunk => {
    stderr += chunk;
    const match = stderr.match(/Authorization URL: (http[^\r\n]+)/);
    if (!authorize || approved || !match) return;
    approved = true;
    void (async () => {
      const authorization = new URL(match[1]);
      assert.equal(authorization.searchParams.get('client_id'), 'tutorial-client');
      assert.equal(authorization.searchParams.get('scope'), 'read');
      const callback = new URL(authorization.searchParams.get('redirect_uri'));
      assert.equal(callback.hostname, '127.0.0.1');
      const code = randomUUID(); codes.set(code, authorization.searchParams);
      callback.searchParams.set('code', code);
      callback.searchParams.set('state', authorization.searchParams.get('state'));
      callback.searchParams.set('iss', origin);
      assert.equal((await fetch(callback)).status, 200);
    })().catch(error => { child.kill(); reject(error); });
  });
  child.on('error', error => { clearTimeout(timer); reject(error); });
  child.on('close', code => {
    clearTimeout(timer);
    try { assert.equal(code, 0, stderr); assert.ok(!stdout.includes(access) && !stderr.includes(access)); resolve(stdout); } catch (error) { reject(error); }
  });
});
try {
  const guide = await readFile(new URL('../../docs/MCP_FROM_SEARCH.md', import.meta.url), 'utf8');
  const commands = [...guide.matchAll(/^kado mcp .+$/gm)].map(match => match[0].replaceAll('https://mcp.example.com/mcp', endpoint));
  assert.ok(commands.length >= 12);
  const fileCommand = windows
    ? guide.match(/^\[IO\.File\]::WriteAllText.+$/m)[0]
    : guide.match(/^printf .+$/m)[0];
  await run(fileCommand);
  assert.equal(JSON.parse(await readFile(join(work, 'args.json'), 'utf8')).text, 'hello ü');
  await run(`kado mcp login '${endpoint}' --profile work --scope read --no-browser --callback-port 0`, true);
  // The first anonymous examples become profile-backed after explicit login.
  for (const command of commands) {
    if (command.startsWith('kado mcp login ')) continue;
    const authenticated = command.includes(endpoint) && !command.includes('--profile') ? `${command} --profile work` : command;
    const output = await run(authenticated);
    if (command.includes('tools-get')) { const schema = JSON.parse(output); assert.ok(JSON.stringify(schema).includes('inputSchema')); }
    if (command.includes('tools-call')) assert.ok(output.includes('tutorial completed'));
  }
  assert.equal(calls.length, 3);
  console.log(JSON.stringify({ platform: process.platform, shell: windows ? 'Windows PowerShell' : 'POSIX sh', commands: commands.length, calls: calls.length, profileReuse: true, namedSessionClosed: true, profileLoggedOut: true }));
} finally {
  await run("kado mcp close '@work'").catch(() => {});
  for (const socket of sockets) socket.destroy();
  await new Promise(resolve => server.close(resolve));
}

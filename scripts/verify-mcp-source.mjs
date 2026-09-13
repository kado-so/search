// Validate build inputs before installing dependencies or running component code.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';

const root = resolve(process.argv[2]);
const lock = JSON.parse(readFileSync(process.argv[3], 'utf8'));
assert.equal(lock.schema_version, 'kado.mcp-source-lock.v1');
assert.equal(lock.repository, 'kado-so/mcp');
assert.match(lock.commit, /^[a-f0-9]{40}$/);
assert.equal(execFileSync('git', ['rev-parse', 'HEAD'], {cwd: root, encoding: 'utf8'}).trim(), lock.commit);
assert.equal(createHash('sha256').update(readFileSync(join(root, 'pnpm-lock.yaml'))).digest('hex'), lock.lock_sha256);
assert.equal(JSON.parse(readFileSync(join(root, 'package.json'), 'utf8')).version, lock.version);
assert.equal(readFileSync(join(root, '.node-version'), 'utf8').trim(), lock.node_version);
assert.equal(process.versions.node, lock.node_version);
assert.equal(`${process.platform}-${process.arch}`, process.env.EXPECTED_TARGET);
assert.equal(execFileSync('git', ['status', '--porcelain', '--untracked-files=all'], {cwd: root, encoding: 'utf8'}).trim(), '');
console.log(`Verified MCP ${lock.commit} on ${process.env.EXPECTED_TARGET}`);

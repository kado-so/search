import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const fixturePath = process.argv[2];
const protocolPath = process.argv[3];
assert.ok(fixturePath, "Go credential fixture path is required");
assert.ok(protocolPath, "Phase 02B protocol validator path is required");

const fixture = JSON.parse(await readFile(fixturePath, "utf8"));
const protocol = await import(pathToFileURL(protocolPath).href);
const verified = protocol.verifyAgentCredentialJws(JSON.stringify(fixture.request), {
  issuer: fixture.issuer,
  nowSeconds: fixture.now_seconds,
});

assert.equal(verified.thumbprint, fixture.thumbprint);
assert.equal(verified.nonce, fixture.nonce);
assert.deepEqual(verified.publicJwk, fixture.public_jwk);
assert.deepEqual(verified.payload, fixture.payload);

import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const fixturePath = process.argv[2];
const protocolPath = process.argv[3];
assert.ok(fixturePath, "signed fixture path is required");
assert.ok(protocolPath, "Phase 02B protocol validator path is required");

const fixture = JSON.parse(await readFile(fixturePath, "utf8"));
const protocol = await import(pathToFileURL(protocolPath).href);
const verified = protocol.verifyAgentEnrollmentJws(JSON.stringify(fixture.request), {
	issuer: fixture.issuer,
	nowSeconds: fixture.verification_time,
});

assert.deepEqual(verified.publicJwk, fixture.management_jwk);
assert.equal(verified.thumbprint, fixture.management_jwk_thumbprint);
assert.equal(verified.nonce, fixture.expected.nonce);
assert.deepEqual(verified.payload, fixture.expected.payload);

import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const fixturePath = process.argv[2];
const tokenPath = process.argv[3];
assert.ok(fixturePath, "Go private_key_jwt fixture path is required");
assert.ok(tokenPath, "Phase 02B token validator path is required");

const fixture = JSON.parse(await readFile(fixturePath, "utf8"));
const token = await import(pathToFileURL(tokenPath).href);
const parsed = token.parseClientAssertion(fixture.assertion);
assert.equal(parsed.kid, fixture.key_id);
const claims = token.verifyClientAssertion(parsed, {
  publicJwk: fixture.public_jwk,
  clientId: fixture.client_id,
  issuer: fixture.issuer,
  nowSeconds: fixture.now_seconds,
});
assert.equal(claims.iss, fixture.client_id);
assert.equal(claims.sub, fixture.client_id);

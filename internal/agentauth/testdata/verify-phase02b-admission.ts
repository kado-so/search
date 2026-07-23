import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const fixturePath = process.argv[2];
const admissionPath = process.argv[3];
assert.ok(fixturePath, "Go admission fixture path is required");
assert.ok(admissionPath, "Phase 02B admission validator path is required");

const fixture = JSON.parse(await readFile(fixturePath, "utf8"));
const admission = await import(pathToFileURL(admissionPath).href);
const binding = admission.encodeAdmissionBinding(fixture.binding_input);

assert.equal(binding.toString("base64url"), fixture.binding_statement);
assert.equal(
  admission.verifyArgon2idAdmissionSolution(
    binding,
    fixture.challenge,
    fixture.solution,
    Buffer.from(fixture.hmac_secret, "base64url"),
  ),
  true,
);
admission.verifyAdmissionBindingProof(fixture.management_proof, {
  publicJwk: fixture.management_jwk,
  type: admission.agentAdmissionManagementProofType,
  endpoint: fixture.endpoint,
  binding,
});
admission.verifyAdmissionBindingProof(fixture.session_proof, {
  publicJwk: fixture.session_jwk,
  type: admission.agentAdmissionSessionProofType,
  endpoint: fixture.endpoint,
  binding,
});

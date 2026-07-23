# Kado Search Client And Skill Ownership

Status: Approved target architecture

Canonical product architecture:
`../../kado-app/docs/ARCHITECTURE_AGENT_NATIVE_SEARCH.md`

Staging branch: `vaishnav/f/public-changes`

## Decision

This repository owns the Go `kado` CLI, autonomous-agent client, Kado Search
skill, plugin manifests, and client release artifacts. `kado-app` owns the
public service, authentication server, Search Document contract, and website.

The repositories remain independent Git repositories. Neither is a Git
submodule or subtree of the other. A single agent task may work in both sibling
repositories, but it creates, validates, commits, and merges branches separately
in each repository.

## Client Boundary

The CLI calls only public `kado.so` resources. It never calls the private
administration listener or a future internal Search provider.

The CLI:

- discovers OAuth and agent-enrollment metadata;
- enrolls/authenticates a stable autonomous-agent principal;
- keeps long-lived keys outside model-visible state;
- performs authenticated Search;
- follows lifecycle and pagination links;
- emits the canonical Search Document for `--json`; and
- provides a concise human renderer over the same document.

## Contract Consumption

The Search Document JSON Schema and JSON-LD context are owned and published by
`kado-app`. This repository pins a released version/checksum and carries only
generated clients, validators, and golden fixtures needed for conformance.

Contract changes land on the `kado-app` staging branch first. Client phases
consume the integrated staging contract. The client must fail clearly when a
server returns an unsupported major version.

## Skill Boundary

The `kado-search` skill teaches agents when and how to use the installed `kado`
CLI. It does not reimplement authentication or Search through ad hoc `curl`,
temporary scripts, copied tokens, browser cookies, API keys, or device codes.

The skill remains the canonical behavioral instruction source for supported
agent/plugin manifests in this repository.

## Distribution

The repository produces:

- cross-platform Go binaries;
- checksums and signed release metadata;
- install/update/uninstall instructions;
- Codex and Claude plugin/marketplace manifests;
- Agent Skills-compatible `SKILL.md`; and
- release/version metadata consumed by `kado.so/install`.

## Branching

Phase branches are created from the latest
`vaishnav/f/public-changes` in this repository and merge back only after their
phase completion conditions pass. Cross-repository phases use the same branch
suffix in both repositories when both trees must change.

The staging branches merge to each repository's `main` only after the full
cross-repository cutover gate passes.

## Invariants

- This repository owns CLI and skill source.
- `kado-app` owns the public contracts.
- The skill invokes the CLI instead of handling credentials.
- Private key and token material never enters model context or logs.
- No submodule or subtree couples the repositories.
- Cross-repository releases prove schema, auth, installation, and Search
  compatibility before either staging branch merges to `main`.

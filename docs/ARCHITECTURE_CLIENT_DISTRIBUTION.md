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

Ordinary CLI composition always uses the platform OS keychain. For isolated
executable acceptance only, the product-owned
`KADO_ACCEPTANCE_CREDENTIAL_FILE` environment boundary selects the existing
permission-restricted file-store adapter for both `auth` and `search`. Its
caller must provide one canonical absolute path under an existing non-symlink
`0700` directory; the selector rejects unsafe or ambiguous locations before
network activity. The caller owns the directory lifecycle, while the CLI owns
the credential record and deletes it solely through confirmed revocation or
explicit credential-purging uninstall. Isolated operations serialize on one
non-secret dot-prefixed `0600` lock retained in the same caller-owned
directory. The acceptance runner waits for every CLI process to stop before
removing that entire directory; per-operation lock deletion is forbidden
because it could split exclusion between a current waiter and a newly created
lock. Isolated operations never recreate a directory removed after selection.

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
- `distribution/kado-installation.v1.gen.json`, the one versioned installation
  description for the CLI, skill, plugins, supported targets, approval policy,
  and public service discovery links;
- install/update/uninstall instructions;
- Codex and Claude plugin/marketplace manifests;
- Agent Skills-compatible `SKILL.md`; and
- release/version metadata consumed by `kado.so/install`.

The installation description derives product identity and version from
`distribution/kado-search.manifest.json`, validates against its Draft 2020-12
schema, and has a deterministic checksum manifest. `kado-app` consumes an exact
generated copy and must not redefine its commands or release availability.
Until signed CLI release metadata is actually published, the description stays
explicitly `unpublished` and contains discovery fields rather than fabricated
artifact URLs or checksums.

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

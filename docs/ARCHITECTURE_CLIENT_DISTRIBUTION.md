# Kado Search Client Ownership

## Repository boundary

This repository owns:

- the Go `kado` CLI;
- autonomous-agent authentication and credential storage;
- the Search lifecycle client and output renderers;
- the `kado-search` skill and direct plugin manifests; and
- signed CLI release construction and self-update.

`kado-app` owns the public service, authentication server, Search Document
contract, and website. The repositories remain independent.

## Client boundary

The CLI calls only public canonical `kado.so` resources. It:

- discovers authentication metadata;
- enrolls or authenticates a stable autonomous-agent principal;
- keeps long-lived keys outside model-visible state;
- performs authenticated Search;
- follows server-provided lifecycle and pagination links;
- validates every Search Document; and
- emits canonical JSON, paginated JSONL, or bounded human output.

Ordinary CLI use stores the management key in the operating-system credential
store. A permission-restricted file-store adapter exists only for explicitly
isolated Unix-like acceptance environments.

Enrollment sends a bounded hostname and local operating-system username. No
Git identity, process tree, environment-based runtime detection, or browser
state is collected.

## Contract consumption

The Search Document JSON Schema, JSON-LD context, semantic rules, and fixtures
are owned and published by `kado-app`. This repository pins released assets and
validates them locally at runtime. Unsupported major versions fail clearly.

## Skill boundary

The `kado-search` skill contains Search guidance only: when to search, how to
form a query, how to invoke Search, and how to use the results. Authentication,
installation, updates, releases, and uninstallation are CLI or operator
concerns and do not belong in the skill.

## Distribution

Plugin and marketplace manifests are maintained directly. CLI release identity
is stored in `distribution/release.json`.

The release builder creates cross-platform binaries, deterministic archives,
checksums, signed release metadata, SBOMs, provenance, and local
install/uninstall scripts. Installed binaries use those artifacts for verified
self-update.

## Invariants

- The skill invokes the CLI instead of handling credentials or HTTP.
- Private keys and tokens never enter model context or ordinary logs.
- JSON-LD and schema validation remain local and deterministic.
- CLI updates verify signed metadata and the replacement executable before
  installation.

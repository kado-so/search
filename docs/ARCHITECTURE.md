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
- performs authenticated Search against kado-app's `/search` resource;
- validates product-execution and pinned public Search links while keeping
  lifecycle operations on the private authenticated resource;
- validates every Search Document; and
- emits canonical JSON, paginated JSONL, or bounded human output.

Each canonical calling agent has an independent management key. Ordinary CLI
use stores it in the operating-system credential store. A configurable,
permission-restricted file backend is available on macOS, Windows, and Linux;
Windows file records are also protected with DPAPI.

The CLI maintains a random non-secret host ID beside its configuration.
Enrollment sends that ID, a bounded hostname and local username, and the
canonical calling-agent name. Agent detection examines process ancestry and
recognized environment markers locally. Only the resulting name is sent; raw
process and environment data, Git identity, and browser state are never sent.

## Contract consumption

The Search Document JSON Schema, JSON-LD context, semantic rules, and fixtures
are owned and published by `kado-app`. This repository embeds the three runtime
contract artifacts and keeps conformance fixtures in test-only data.
Unsupported major versions fail clearly.

## Skill boundaries

The `kado` skill contains general CLI and account-linking guidance. The
`kado-search` skill contains only Search routing, query formation, invocation,
and result-use guidance. Installation, releases, and uninstallation remain CLI
or operator concerns.

## Distribution

The CLI embeds a revisioned catalog plus every active skill variant so an agent
can install compatible units without a plugin manager. A signed remote catalog
is authoritative when available, and exact agent variants take precedence over
the default variant. Installation targets all detected
harness locations plus `~/.agents/skills` by default. Kado scans every known
destination and accepts a local ownership receipt only after its canonical path
and actual content digest verify; previously registered copies must also match
the registry. Every verified Kado-managed copy is synchronized after a
successful direct CLI update. Plugin and marketplace manifests remain optional
secondary channels.

The release operator provides the semantic version explicitly; product
identity is fixed in the release builder.

The release builder creates cross-platform binaries, deterministic archives,
and signed archive metadata for self-update. Checksums, SBOMs, provenance, and
local install/uninstall scripts are standalone operator artifacts.

Direct installation from `kado.so` is the primary path and must be executable
by an agent without a package manager or language runtime. Package-manager
installations remain owned and updated by their package manager. Windows direct
updates hand replacement to a verified helper process after the running CLI
exits.

## Invariants

- The skill invokes the CLI instead of handling credentials or HTTP.
- Every agent-authenticated command uses the shared agent-session middleware;
  missing credentials follow its single autonomous create-and-login path.
- Private keys and tokens never enter model context or ordinary logs.
- JSON-LD and schema validation remain local and deterministic.
- CLI updates verify signed archive metadata and the replacement executable
  identity before installation.
- Kado never overwrites a skill or executable owned by another installer.

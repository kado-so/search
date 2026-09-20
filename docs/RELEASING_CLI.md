# Releasing the Kado CLI

This is a public overview for maintainers. Production access and operational
configuration are managed outside this repository.

## Preconditions

- Start from a reviewed, clean commit on `main`.
- Keep bundled component sources and dependencies locked to reviewed versions.
- Run the repository tests, static checks, and native qualification workflows.
- Prepare release notes describing user-visible behavior and compatibility.

## Release process

Releases are created through the reviewed release workflow from a semantic
version tag. The workflow builds supported targets, produces signed release
metadata, verifies the resulting artifacts, runs native checks, and publishes
immutable versioned assets before promoting the stable channel.

Production signing material must never be placed in source control, command
arguments, artifacts, or logs. Production releases must not be assembled or
signed through an unreviewed local process.

## Verification

After publication, verify the public metadata and artifacts, then test a fresh
installation on supported platforms. Check version/help output, authentication,
Search, MCP and A2A invocation, update, rollback, and uninstall behavior.

If publication is interrupted, stop promotion and inspect the published state.
Never replace an immutable version with different bytes. Recover by restoring a
previous signed channel or publishing a reviewed forward fix through the same
release workflow.

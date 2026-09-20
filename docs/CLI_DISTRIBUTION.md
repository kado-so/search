# CLI distribution

Kado is distributed as a native CLI for Linux, macOS, and Windows. Direct
installation from [kado.so/install](https://kado.so/install) is the recommended
path and does not require a language runtime or package manager.

## Installation ownership

Direct installations are updated and removed with `kado update` and
`kado uninstall`. Package-managed installations remain owned by their package
manager and do not self-update.

Uninstall preserves configuration and credentials by default. Credential
removal requires an explicit request.

## Release integrity

Release artifacts are versioned and authenticated before installation. Kado
verifies a complete release as one unit and rejects missing, modified, or
unexpected files.

Updates activate a verified complete release only after it has been installed
successfully. Rollback selects a previously verified release; components are
not mixed between versions.

See [installation documentation](INSTALL.md) for supported installation and
maintenance commands.

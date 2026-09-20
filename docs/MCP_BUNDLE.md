# MCP bundle security

Kado releases include the Kado CLI and the compatible A2A and MCP components
needed by that version.

Each release contains a signed inventory of its files. Before using bundled
components, Kado verifies their identity and rejects missing, modified,
unexpected, or unsafe archive entries.

Updates and rollback operate on complete verified releases. Kado does not mix
executables or runtime files from different versions, and bundled components
are not selected from the current directory or system `PATH`.

Configuration, provider profiles, and Kado credentials are stored outside the
managed release and are preserved by default during update or uninstall.
Package-managed installations remain under their package manager's control.

For MCP usage and session cleanup, see [MCP from Search](MCP_FROM_SEARCH.md).

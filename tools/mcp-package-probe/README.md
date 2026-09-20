# MCP payload qualification harness

This development-only command verifies a built MCP payload and exercises its
process boundary. It is not installed with the public Kado CLI.

Build it with:

```text
go build -trimpath -o bin/mcp-package-probe ./tools/mcp-package-probe
```

Run it against a trusted payload manifest:

```text
mcp-package-probe -bundle <absolute-directory> -digest <manifest-sha256> -entry cli -- --version --json
```

The digest must come from the trusted build output rather than from the payload
being inspected. Qualification roles and fixtures are test-only.

Run the package checks with:

```text
go test ./tools/mcp-package-probe
go vet ./tools/mcp-package-probe
```

The tests cover command forwarding, output and exit-code parity, tamper
rejection, child-process behavior, and packaged session lifecycle. MCP protocol
parsing remains owned by the bundled MCP component.

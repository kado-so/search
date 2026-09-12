# MCP payload qualification harness

Owner: Search CLI distribution. This development command exercises G2's proposed
whole-payload verification and process boundary; it is not installed or wired into
`kado mcp`, the stable launcher, activation or update code.

Build with `go build -trimpath -o bin/mcp-package-probe.exe ./tools/mcp-package-probe`
(omit `.exe` on Unix). Run `go test ./tools/mcp-package-probe` and
`go vet ./tools/mcp-package-probe` for the verifier checks.

```text
mcp-package-probe -bundle <absolute-directory> -digest <trusted-manifest-sha256> -entry cli -- --version --json
```

The trusted digest must come from the separately reviewed builder output, not an
untrusted replacement manifest. `verify`, `probe`, `fixture` and `bridge` roles
exist only for qualification. Foreground mode contains representative child
lifetimes; persistent mode allows the named MCP bridge to outlive its creating
command. Full lifecycle hardening and production manifest authentication remain
later goals.

Runtime construction, fixture and complete status are owned by
`mcp/scripts/packaging` and `mcp/docs/G2_PACKAGING.md`. G2 passed all six native
targets and received final approval. G3 adds an opt-in command forwarding check:
set `KADO_MCP_QUALIFICATION_BUNDLE` to a freshly built native payload, then run
`go test ./tools/mcp-package-probe -run TestMCPCommandBoundaryCandidate -v`.
It compares direct private-Node execution with Go dispatch for help, unknown
future commands, query/Unicode/metacharacter argv and piped JSON validation,
including exact stdout, stderr and exit status. This does not install the G9
public namespace. No MCP parsing logic belongs in the Go harness.

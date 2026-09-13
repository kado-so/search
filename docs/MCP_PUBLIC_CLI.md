# Public MCP CLI integration (G9)

Status: implemented, locally validated and finally approved by the user on
2026-09-13 for commit/push. GitHub compute quota is deferred by the user.

## Public command

`kado mcp` invokes the maintained MCP component from the selected signed Kado
bundle. It needs no separately installed Node, npm package or provider plugin.

```text
kado mcp --help
kado help mcp connect
kado mcp tools-list https://mcp.example.com/mcp --json
kado mcp tools-call https://mcp.example.com/mcp echo --args-file args.json --json
kado mcp connect https://mcp.example.com/mcp @example
kado mcp @example tools-list --json
kado mcp close @example
kado version --json
kado completion powershell
```

The example endpoint is a placeholder; qualification uses a controlled local
MCP peer with isolated state. Provider authentication remains owned by MCP.

Go recognizes only the namespace, optional leading Kado `--agent`, the
`help mcp` alias, and the shell-completion marker. Everything after `mcp` is
opaque, including unknown future flags, empty arguments and MCP options that
share names with Kado options. Streams and component exit codes pass through.
Unix replaces the process with the private Node executable; Windows inherits
console/stdio and waits for the component. Named sessions remain owned by the
authenticated native session supervisor from G7. The A2A non-breakaway job is
not applied to MCP, since that would destroy sessions when the caller exits.

The dispatcher resolves the physical executable through filesystem aliases,
authenticates the complete payload, checks its stamped component identity and
uses only signed Node/CLI entry roles. Node injection environment settings are
removed and global module search is disabled. See [bundle contract](MCP_BUNDLE.md).
The launcher, A2A and MCP share the executable-path resolver. Windows resolves
the open file handle through `GetFinalPathNameByHandle`; the real junction test
exposed a failure when using Go's symlink walker for that handoff. Unix retains
physical symlink resolution. Whole-payload verification still rejects redirected
paths inside the signed tree.

Human help and command hints use `kado mcp`; legal attribution and private
component filenames retain their identities. Existing protocol JSON fields are
unchanged. `kado version` includes MCP source/lock and Node identity; JSON retains
G8's `kado.version.v2`. `kado mcp --version` reports the component version.

Bash, Zsh, Fish and PowerShell use Cobra's completion protocol. MCP completions
come from the same static Commander metadata used by the runtime. Completion
never authenticates, enumerates profiles, opens a server, reads stdin, creates
state or executes a handler. It offers commands/flags, excluding hidden payment
options; it does not discover live tool names or secret values. Missing/tampered
payloads produce only the error directive with exit 0 and no stderr, preventing
shell diagnostic noise or fallback to unverified code.

## Distribution and lifecycle

Every release-builder channel requires the exact MCP commit and independently
pinned target component digests. Each archive contains Kado, A2A, private Node,
MCP code/dependencies/native host, licenses and signed payload inventory. The
builder emits the complete SBOM and provenance, plus signed package checksums.

| Channel | Payload and public entry | Lifecycle owner |
| --- | --- | --- |
| Direct / agent-first | Stable `kado`, immutable complete versions under `kado[.exe].d` | Signed Kado installer/update/uninstall APIs |
| Homebrew | Complete private `libexec`; only public `bin/kado` symlink | Homebrew |
| WinGet | Signed `payload/` subdirectory; only nested `kado.exe` gets the `kado` alias | WinGet |
| Scoop | Signed `payload/` subdirectory; only `payload/kado.exe` gets a shim | Scoop |
| deb | Complete `/usr/libexec/kado`; `/usr/bin/kado` symlink | dpkg/apt |
| rpm | Complete private libexec; public Kado symlink; binary rewriting/stripping disabled | rpm/dnf |
| Container | Complete private payload in a pinned Debian base with required libraries | Image rebuild |

Windows managers may add receipts outside `payload/`; extra files inside the
signed tree still fail verification. Package archives contain their signed
installation-owner receipt. `kado update` and `kado uninstall` refuse package-
owned installations with owner-specific instructions. Close named MCP sessions
before replacing/removing a package or container; Kado does not override the
package manager or scan other users' credential stores. Direct installs use G8's
registered-home inventory, active-version retention and authenticated close.

Direct installers download the metadata, signature, archive and matching HTTPS
bootstrap executable. The bootstrap verifies its identity and uses the bounded
signed Go extractor/installer through a private entrypoint. No shell archive
extractor publishes executable components. Existing installations use `kado
update`; skills/auth setup still follows successful install/update.

Direct updates and rollback keep the complete-unit activation, same-version
collision refusal, repair, retention and credential behavior from G8. There is
no legacy-install migration. Uninstall scripts delegate removal to the CLI.

On Windows a verified external helper waits on inherited handles to the invoking
processes, then performs authenticated whole-install removal. The public command
reports `uninstall pending; result: <path>`, not success. The helper atomically
publishes a `kado.uninstall.v1` result; `uninstall.ps1` waits and reports the result.
The helper deletes only its own temporary executable/directory after exit. It
does not trust recorded PIDs or issue unchecked recursive payload deletion.
The small status receipt remains outside the removed installation. Credentials
are preserved by default; explicit Kado credential revocation retains its
existing contract.

## Validation and remaining release gates

Local checks on 2026-09-13:

- Search `go test ./...` passed. After the alias-path fix, launcher, A2A, CLI and
  MCP-dispatch regressions and relevant Go vet passed again.
- MCP build and unit suite passed (1,007 tests; seven platform skips). Native
  stdio smoke and focused completion/help tests passed. ESLint reported zero
  errors and three existing non-null-assertion warnings.
- All six public executable targets cross-built. The complete public-bundle
  test passed on Windows x64 in 214.23 seconds and Linux x64 (WSL) in 145.31
  seconds. Linux resolver/launcher/A2A regressions also passed after extracting
  the shared resolver. No owned test processes remained after Windows cleanup.
- Generated installer/uninstaller tests passed in PowerShell and POSIX sh.
  Package archive tests verified signed complete contents, outer manager
  metadata, owner receipts, SBOM/provenance and tamper rejection.
- MCP's locked source/archive/license checks passed; changed/new text is UTF-8
  with LF. Existing `.gitattributes` enforces LF in all three repositories.

Qualification component manifests:

| Native input | `payload.gen.json` SHA-256 |
| --- | --- |
| Windows x64 | `fcc84bc5a109cddeb78625d862990243f836c400cfcb49a24eb0bbe8fd031686` |
| Linux x64 | `dfa507c378ca034904db84cb33445697124259034f9b908bad456ebaca4208f9` |

The actual public-bundle test is
`go test ./internal/releaseclient -run '^TestPublicMCPCompleteBundle$' -count=1 -v`
with `KADO_MCP_QUALIFICATION_BUNDLE` pointing to a native MCP qualification payload
created by `mcp/scripts/packaging/build.mjs`. It builds a real stamped Kado binary
and signs a complete local test release. It exercises fresh install, private/public
output parity, nested help, all four completion script formats, quiet corruption
failure, aliases, package ownership, persistent stdio invocation with piped Unicode
input, and public uninstall with preserved isolated credential sentinels.

The six-target MCP qualification workflow already runs the releaseclient package
with that environment, so it also runs this public test. No GitHub run is required
just to record local success. Qualification artifacts use a public test key and
fixture component identities; they are not publishable releases.

G14 must wire reviewed six-target MCP release inputs into Search's candidate/
publication workflows, which still expect the earlier pair-only invocation of
the release builder. That old invocation fails closed because MCP inputs are now
mandatory. Final native package-manager installs, macOS/ARM64 execution, console
signal tests and release-candidate qualification remain release gates; local
cross-builds are not substitutes for those runs. No release was published.

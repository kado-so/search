# MCP direct-release readiness

Updated 2026-09-14. Implementation preparation and commit/push/merge are approved. Public release
publication awaits the agreed final approval. Do not tag a public release yet.
This record supersedes older G14 readiness statements; earlier evidence remains
historical, not evidence for changed final bytes.

## Scope and source freeze

| Item | Current decision/status |
| --- | --- |
| CLI | Prepare 0.2.0; public stable was last verified at 0.1.22. |
| MCP | Prepare component 0.1.0; package remains private, no npm release. |
| Node | Preserve pinned vendor 24.20.0 and the frozen dependency graph. |
| A2A | Preserve fdbb3aad6289c0070480147a29e04c3d46aa6c2f. |
| Search base | main f4c97c2465fd4fbdd9eaf75951d55d61d8cdef4c. |
| MCP source | main 77c43f69bd97a33b5d8c7e8f5a49bafc2e942891 (squash merges of PRs 2 and 3). |
| Work branches | ayush/f/mcp-direct-release in Search, MCP and search-pipeline. |
| Exact component pin | `third_party/mcp/source.lock.json` pins MCP 0.1.0 at 77c43f69bd97a33b5d8c7e8f5a49bafc2e942891, with the frozen dependency-lock digest and Node pin. |
| Distribution | Direct archives, GitHub release, kado.so installers and four-skill catalog. Homebrew, Scoop, WinGet, deb/rpm and container publishing/qualification deferred. |

MCP PRs 2 and 3 are squash-merged and pinned. Merge Search after its required checks,
then the pipeline readiness-document PR; use squash merges throughout. Pipeline needs documentation only, and kado-app needs no additional
runtime release. The final Search commit and component digest index must be
recorded before signing the public candidate. No guessed or dirty source pin.

## CI and local verification

[Search PR 22 qualification](https://github.com/kado-so/search/actions/runs/34785045050)
passed all 31 jobs. Its
[main-branch retry](https://github.com/kado-so/search/actions/runs/34808359316)
also completed successfully, all 31 jobs, on 2026-09-14.

The failed first main attempt hit Windows disk-flush cost: the launcher package
reached Go's default 10-minute alarm inside `FlushFileBuffers` while staging the
uninstall maintenance tree; a concurrent releaseclient install exceeded three
minutes. This is observed I/O contention evidence, not proof that every timeout
has the same cause. The preparation patch runs these large packages sequentially
with a 15-minute Go deadline and 16-minute outer watchdog for each. Production
verification, fsync and command deadlines remain unchanged. Validate this patch
on hosted native runners after its reviewed commit; the old green run does not
validate the patch.

The final local packaged-runtime smoke also passed relocation to a Unicode/space
path, private Node without Node/npm on PATH, synthetic Credential Manager
write/read/delete, loopback callback, foreground descendant cleanup, named
connect/catalog/schema/call/close, tamper/extra-file/missing-addon rejection and
read-only payload operation. Its sanitized evidence is retained in MCP's ignored
`.kado-dev/direct-release-review-summary.gen.json`. First full verification took
34.3 seconds; three later fresh-process calls took 1.10–1.21 seconds. These are
local measurements, not a performance guarantee or final signed release proof.

The first PR run exposed macOS tar member-name normalization in the newly
enabled archive test. MCP PR 3 fixes the test by extracting normally and
checking the normalized filename and exact contents; production archive bytes
are unchanged. The corrected commit is pinned for the final native rerun.

The CI and release workflows retain direct six-target construction and native
qualification, and omit package-channel build/qualification jobs. Ordinary tests
of package ownership remain; channel tooling is not deleted.

Local Windows review payload: `.kado-dev/direct-release-review` in MCP, version
0.1.0, 4,382 files. Manifest SHA-256
`5d3309e8ac62482dc4b03590542d4921fc77b21af44671684a3aa30dee9497b1`;
archive SHA-256
`367235f4437c1cc96c7d408415cdef447e23dc48d68987203598b948a97872f8`.
This is the historical pre-commit qualification build, not a clean committed
release component. TypeScript/native-host build and real stdio help/version,
connect/list/schema/call/close passed. The sequential native run passed all tests in `internal/payload`,
`internal/launcher` and `internal/releaseclient`. In particular,
`TestCompleteNativeMCP` passed in 228.52 seconds and
`TestPublicMCPCompleteBundle` in 243.77 seconds, including actual public command
parity, help identity, completion, named invocation, tamper rejection, aliases,
credential preservation and uninstall. Whole-format update/repair/rollback and
retention tests passed. The exact final-release candidate test intentionally
skipped because final committed/signed bytes are not available yet.
`go test ./tools/release ./tools/mcp-components -count=1 -timeout=5m` and their
`go vet` checks passed. Actionlint 1.7.12 passed for the changed workflows. Upstream
source/lock/license verification passed; the stale archive fixture's missing
target was fixed, and archive determinism/Unicode qualification passed and is
now included in native CI. The new Windows payload retains valid OpenJS Node
Authenticode; its keyring addon is unsigned (no new platform-signing claim).

## Native qualification and deferred platform signing

[Node 24.20.0's pinned platform requirements](https://github.com/nodejs/node/blob/v24.20.0/BUILDING.md#platform-list)
remain macOS 13.5; Linux kernel 4.18/glibc 2.28 (libstdc++ GLIBCXX_3.4.25);
and Windows 10/Server 2016 x64 or Windows 10 arm64. Node excludes vendor-EOL
platform versions. These are dependency floors, not Kado's tested support claim.

| Gate | Evidence and remaining work |
| --- | --- |
| Native amd64/arm64 | Existing complete-candidate CI passed Ubuntu 24.04, macOS 15, Windows Server 2025 x64 and Windows 11 arm64. Requalify final committed/signed bytes. |
| Supported OS baseline | Retain the existing Kado release-test baseline: Ubuntu 24.04 on amd64/arm64, macOS 15 on Intel/Apple silicon, Windows Server 2025 amd64 and Windows 11 arm64; local Windows 11 amd64 also passed. Older versions are not claimed by this release. Node dependency floors are not a Kado support promise. Musl/Alpine is not supported. |
| macOS platform signing | Explicitly deferred by the user on 2026-09-14; not a release gate. Cin has existing signing/notarization credentials and successfully notarized both architectures in release run 28287317787, but this release does not adopt that setup. |
| Windows platform signing | Explicitly deferred by the user on 2026-09-14; not a release gate. The published Kado 0.1.22 Windows executable was independently inspected and is unsigned. |
| Credential identity | Recheck the unchanged private vendor runtime/addon through install/update/rollback under existing OS access controls. Preserve user stores and use synthetic entries. No Kado publisher-identity migration is introduced. |
| Release authentication | Retain the existing protected Ed25519 key, signed metadata, complete-payload hashes and verification. Preserve vendor Node signatures. No additional platform-signing transformation is required. |

`KADO_RELEASE_SIGNING_KEY` is configured in the protected `cli-release`
environment. The user's direction is to retain Kado's previous release-signing
approach; Apple signing/notarization and Windows Authenticode are both deferred.
The earlier request for platform-signing credentials is no longer pending.
Public installation and native functionality still need validation, but lack of
these platform signatures is not itself a blocker under this accepted scope.

## Deployment and public release boundary

The private provider contract **1.3.1**, Search runtime, public result mapping,
execution links, production Vespa and corpus changes are already deployed.
See [G14's current deployment record](https://github.com/kado-so/search-pipeline/blob/staging/docs/MCP_G14_REVIEW.md)
for backend evidence after the documentation update is merged.
Production v1/v2 Search-to-Context7 and direct/named MCP invocation passed with
an unpublished candidate. Disposable credentials and canary resources were
removed. Automated crawler/ingestion upgrades and a bulk rebuild remain separate.

The public skill catalog still lacks kado-mcp. The next CLI release promotes the
four already implemented bundled skills together: kado-search,
kado-cli-non-search, kado-a2a and kado-mcp. No skill source was edited here.

A read-only public snapshot of eight installer/channel objects is retained in
Search's ignored `.kado-dev/direct-release-channel-snapshot`, with names, sizes,
SHA-256 values and content types in `snapshot.gen.json`. It confirms CLI 0.1.22
and catalog revision 4. One initial Python download returned 403; all eight
canonical URLs then downloaded successfully using the documented curl client.
This is preparation evidence, not the promotion-time Azure rollback snapshot;
The release workflow now captures an ETag-consistent Azure snapshot and uploads
it as a 90-day rollback artifact before any publication writes. The local
identity cannot read release blobs directly; the workflow uses the existing
release OIDC identity. Controlled snapshot checks passed for all eight objects,
hashes, existing-directory refusal, failed conditional downloads and changes
during capture. Actual Azure capture will run in the approved release job.

Before publication: finish the source freeze, final native qualification and
review the signed candidate; capture channel rollback objects; obtain explicit
publication approval. Then use [RELEASING_CLI.md](RELEASING_CLI.md), verify all
public URLs and perform fresh public installation/OAuth/production Search-to-MCP
checks on the supported native targets. Package-manager work is not a blocker
for this approved direct-release scope.

Operational followups: private MCP Actions billing, Azure command-response
recovery, and rotation of the read-only MCP checkout PAT before 2026-12-13.
These are tracked separately from the completed production deployment.

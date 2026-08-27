# Official A2A CLI source boundary

Kado builds the bundled private A2A sidecar from the exact official source
identified by `upstream.lock.json`. The upstream repository and Git history are
not vendored here.

The lock is closed and records the repository, module, exact commit, optional
release tag, snapshot or release version, source archive and canonical tree
checksums, dependency manifests, Apache-2.0 license, notice-file set, pinned Go
toolchain, display name, and every patch identity. The release tool rejects an
unknown lock field or any mismatch before compilation.

The only local source change is
`patches/0001-configurable-display-name.patch`. It introduces Cobra's display
name annotation while retaining the upstream default `a2a`; Kado release builds
will set the displayed path to `kado a2a` at link time. It does not change A2A
commands, flags, transport, output, validation, or protocol behavior.

To review an upstream update:

1. Fetch all official tags in an isolated checkout and select an exact commit.
2. Prefer a conventional tag that resolves to that commit. If the commit has no
   conventional tag, use a snapshot version containing its seven-character
   commit prefix.
3. Review source, dependencies, license/notices, and patch applicability; then
   update every affected locked identity in one review.
4. Run the release-tool tests and the real-source qualification from the
   repository root:

   ```text
   KADO_A2A_UPSTREAM_SOURCE=/absolute/path/to/isolated/a2a-cli \
     go test ./tools/release -run TestProductionA2ASourcePreparation -count=1 -v
   ```

Snapshot preparation intentionally stops if a conventional release tag appears
at the locked commit. Tagged and snapshot locks otherwise use the same
commit-based verification and preparation path.

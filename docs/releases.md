# CI and releases

CI runs for pushes and pull requests: pinned Go toolchain, dependency verification, Go tests/vet/govulncheck, builds, Node UI tests, Compose validation, Docker build and an isolated container smoke test. No production credentials are required. Action dependencies are pinned to commit SHAs.

Push a reviewed stable `vMAJOR.MINOR.PATCH` tag to trigger release automation. It first requires CI, then builds Linux amd64/arm64 archives containing `inventrospect`, `inventrospect-mcp`, `inventrospect-ops`, licence and documentation. `SHA256SUMS` covers both archives. GitHub release notes are generated from repository history.

The container uses GHCR with the repository owner/name converted to lowercase automatically, not a hard-coded account: `ghcr.io/<owner>/<repository>`. For this repository that is `ghcr.io/sigmatechnica/inventrospect`. Tags are `vMAJOR.MINOR.PATCH`, `MAJOR.MINOR`, and `latest`; the latter two move on each published stable tag. Publish stable versions in increasing order. Prefer exact version tags (or digests) for deployments. Both amd64 and arm64 are in one manifest. Builds use native cross-compilation; no emulation is required.

Only the tag publish job receives contents/package write permissions using GitHub's built-in token. Ordinary PR jobs cannot publish. Enable Actions and package publishing in repository settings; ensure GHCR package visibility is public for an anonymous public quick start. No release has been implied by adding these workflows. Check the first hosted run, both architecture pulls, release assets and anonymous access before announcing availability.

Local archive check: `bash scripts/release-build.sh v0.1.0 "$(git rev-parse HEAD)"`. This creates files only; it does not tag, upload or publish. Publishing jobs are not fully atomic across GHCR and GitHub: inspect any failed release before retrying. Never replace an already-announced version with different contents.

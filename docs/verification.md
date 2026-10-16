# Verification notes

The Session B implementation was checked locally with:

- Full Go suite, including historical schema versions 1–4, migration rollback/newer-schema rejection, connection replacement PRAGMAs, exclusive directory ownership/crash release, independent cleanup, conservative orphan handling and offline backup/restore integrity tests.
- `go vet ./...`, dependency-free Node explorer tests and `govulncheck` v1.9.0. The vulnerability scan found no affected code or imported-package vulnerabilities; it reported 21 non-reachable findings in required modules.
- Linux amd64 and arm64 builds of all three binaries; version stamping and archive checksums.
- Official standalone Docker Compose configuration validation (including the maintenance profile).
- Real amd64 executable startup/bootstrap, REST and MCP HTTP discovery/search/document bytes, scope rejection, SIGTERM shutdown, restart persistence and restore into a fresh directory. The built MCP stdio executable also passed its integration test.

The amd64 image built with Podman in Docker image format (including the health check). Official Compose connected to Podman's Docker-compatible API passed the isolated container smoke test: UID 65532, bootstrap, healthy app/MCP, invalid-token rejection, real REST/MCP document retrieval, persistence across container recreation, stopped backup/verification and restore into a fresh volume with login and document checks. These are Podman results, not a claim of Docker Engine runtime testing.

Follow-up on 2026-10-16: Docker Engine 29.4.2 with Compose 5.1.2 passed the isolated container smoke test after changing the application network from `internal: true` to a normal bridge. The internal-only network had left both containers healthy but suppressed host-port publication on Docker. The corrected configuration preserves loopback bindings. Verified fresh bootstrap, host reachability, non-root execution, MCP authentication/discovery/document bytes and scope rejection, persistence after recreation, and backup/restore into a fresh volume. The existing installation retained its data volume and remained healthy.

The maintainer reports that GitHub Actions is now installed and CI runs; its hosted result has not been independently checked here. GHCR publishing/public visibility and arm64 runtime execution remain unverified. Cross-compilation is not an arm64 runtime test.

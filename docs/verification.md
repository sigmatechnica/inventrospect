# Verification notes

The Session B implementation was checked locally with:

- Full Go suite, including historical schema versions 1–4, migration rollback/newer-schema rejection, connection replacement PRAGMAs, exclusive directory ownership/crash release, independent cleanup, conservative orphan handling and offline backup/restore integrity tests.
- `go vet ./...`, dependency-free Node explorer tests and `govulncheck` v1.9.0. The vulnerability scan found no affected code or imported-package vulnerabilities; it reported 21 non-reachable findings in required modules.
- Linux amd64 and arm64 builds of all three binaries; version stamping and archive checksums.
- Official standalone Docker Compose configuration validation (including the maintenance profile).
- Real amd64 executable startup/bootstrap, REST and MCP HTTP discovery/search/document bytes, scope rejection, SIGTERM shutdown, restart persistence and restore into a fresh directory. The built MCP stdio executable also passed its integration test.

The amd64 image built with Podman in Docker image format (including the health check). Official Compose connected to Podman's Docker-compatible API passed the isolated container smoke test: UID 65532, bootstrap, healthy app/MCP, invalid-token rejection, real REST/MCP document retrieval, persistence across container recreation, stopped backup/verification and restore into a fresh volume with login and document checks. These are Podman results, not a claim of Docker Engine runtime testing.

Hosted GitHub Actions, GHCR publishing and public package visibility cannot be verified until the workflows run in the repository. Cross-compilation is not an arm64 runtime test. Docker Engine was not installed on the development host.

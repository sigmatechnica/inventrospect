# Contributing

Use Go from `go.mod` and Node 24. Run the Go tests, vet, govulncheck and dependency-free UI tests listed in the README. For deployment changes also run `docker compose config --quiet`, build the image and run `bash scripts/container-smoke.sh` on a disposable Docker host. The smoke test uses its own project and removes only its volumes.

Keep changes focused and preserve REST/MCP compatibility. Add regression tests for changed behaviour. Do not commit databases, uploads, credentials, environment files or backup archives. Describe tests actually run and any unavailable prerequisites in pull requests. Contributions remain under the repository's AGPL-3.0 licence. Report security concerns privately as described in SECURITY.md.

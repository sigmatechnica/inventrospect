# Security

Use GitHub private vulnerability reporting on this repository's Security tab when enabled. If unavailable, ask maintainers for a private reporting channel without disclosing exploit details in a public issue. Include affected version, reproduction and impact; never attach live tokens, passwords or private inventory. There is no guaranteed response SLA or published long-term support schedule yet.

Self-hosters should use supported patched releases, HTTPS for remote access, restricted backend ports, least-privilege agent tokens and regularly tested backups. An admin-owned deletion token can permanently remove inventory and documents. Revoking a token does not restore deleted data. Protect backup archives as sensitive credentials and keep copies off-host.

Before the first public release, maintainers should enable private vulnerability reporting, verify CI and multi-architecture release publishing, and review anonymous GHCR access. The application does not collect telemetry or provide a hosted identity service.

# Deployment and operations

## Layout and configuration

Docker Compose runs two processes in separate containers, using the same image:

| Service | Interface | Persistent storage |
| --- | --- | --- |
| app | HTTP 8080, REST and web | `data:/data` |
| mcp | Streamable HTTP 8081 `/mcp`; REST calls to `app:8080` | None |
| backup (maintenance profile) | Offline command, no network | `data:/data`, `backups:/backups` |

The runtime is distroless Debian with CA certificates, no shell or package manager, and UID/GID **65532:65532**. Binaries are statically built with the existing pure-Go SQLite driver for Linux amd64/arm64. Runtime filesystems are read-only except mounted state and temporary storage. Logs go to stdout/stderr with bounded Docker log rotation. There is no configurable log level, public base URL or external service dependency.

`/data/inventory.db`, its SQLite WAL/SHM files, and `/data/documents/` contain all persistent application state. There are no additional signing keys or external configuration secrets. `.inventrospect.lock` is an advisory kernel lock, not a PID file. Never unlink it while a process is running. Crashes release the lock automatically. Use a local filesystem supporting flock and SQLite locking; NFS/SMB/shared multi-host volumes are unsupported. Never mount one data directory into two application instances. MCP never opens it.

For bind mounts, create private directories owned by numeric UID/GID 65532 before startup. Named volumes inherit image directory ownership automatically. Docker rootless/user-namespace deployments may require mapped host ownership. Keep sufficient free disk space for SQLite, attachments and backups. Verification extracts a complete archive temporarily; the maintenance service uses `/backups` for temporary files to avoid the application's 256 MiB temporary-memory limit.

Copy `.env.example` for published port, bind address, secure-cookie and shutdown settings. Native binaries also accept `-data`, `-addr`, `-shutdown-timeout`, `-max-bulk-records`, `-agent-writes-per-minute`; use `-help` for exact flags. `-version` prints build metadata (ops uses `version`).

Bootstrap using the password-on-stdin command in the README. To select a different initial name add `-e INVENTROSPECT_ADMIN_USER=yourname` to `compose run`. Legacy `INVENTROSPECT_ADMIN_PASSWORD` remains supported for one-time setup; remove it immediately afterward from the environment and any orchestration configuration. Never put passwords or bearer tokens in committed `.env` files or command arguments.

## Network and HTTPS

- **Local:** defaults publish only `127.0.0.1:8080` and `127.0.0.1:8081`. Plain HTTP cookies work locally.
- **LAN:** changing `BIND_ADDRESS` deliberately exposes plaintext HTTP unless you provide TLS termination. Anyone on an untrusted network could intercept credentials. Prefer HTTPS even on LANs; restrict access with firewall rules.
- **Remote:** use a trusted HTTPS reverse proxy. Keep the published ports on loopback when the proxy runs on the host. Proxy the application hostname to 8080, and a separate MCP hostname to 8081, preserving `/mcp`, `Host`, `Authorization`, streaming responses and sufficient request timeouts. Restrict backend reachability to that proxy. Set `SECURE_COOKIES=true` and recreate the app. The browser must then use HTTPS.

Forwarded headers (`Forwarded`, `X-Forwarded-*`) are **not trusted or consumed**. There is no proxy trust list because no forwarded identity is accepted. Preserve the original Host header so same-origin checks work. Client-IP controls see the proxy's address; do not rely on spoofable forwarded addresses. Secure cookies are explicitly configured rather than inferred from headers. HSTS, if desired, belongs at the HTTPS proxy and must not be applied indiscriminately to plain HTTP. Internal Docker HTTP is not public HTTPS: Compose explicitly permits HTTP only for its private bridge; host publication determines external exposure.

Native executables reject non-loopback listeners unless explicitly configured for HTTPS termination (`-secure-cookies` for app, `-external-https` for MCP) or `-allow-insecure-http`. The latter is deliberately enabled for the private Compose bridge. Changing a published bind address is an explicit operator decision; it does not add TLS.

## Health and shutdown

Unauthenticated `GET /healthz` checks process liveness; `GET /readyz` checks the database (app) or application readiness (MCP). Responses contain no internal details. Readiness has a bounded timeout and a lightweight database ping. Both return 503 while draining. Docker probes readiness with the built-in ops executable, without curl or a shell.

SIGTERM stops accepting requests and waits up to `SHUTDOWN_TIMEOUT` (default 15 seconds) for active requests. Compose allows 30 seconds before SIGKILL. Keep the Compose grace period longer than any configured application timeout. A timeout fails the process; SQLite recovery and the kernel lock handle a crash, but clients should retry with their normal idempotency keys. Migration failure stops startup. Each migration is transactional; newer unsupported schemas are rejected.

## Backup

Stop application and MCP to obtain a consistent snapshot of both database and documents. Maintenance acquires the same exclusive data lock and refuses an active server. The archive includes WAL/SHM when present, all document files, and a SHA-256 manifest. Unknown/special files, missing documents and failed integrity checks fail the command; they are never silently skipped.

From Bash, save a private archive **outside** Docker volumes:

```bash
set -e
umask 077
docker compose stop mcp app
snapshot="inventrospect-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
docker compose run -T --rm backup backup -archive=- > "$snapshot.partial"
mv "$snapshot.partial" "$snapshot"
docker compose run -T --rm backup verify -archive=- < "$snapshot"
docker compose up -d
```

A failed command leaves the application stopped and may leave a `.partial` output; do not treat that as a successful backup. Inspect the error and restart explicitly when safe. Copy completed archives to separate, protected storage. They contain password hashes, sessions and private documents. The manifest detects accidental corruption; it is not a cryptographic signature proving origin.

Alternatively, `docker compose run --rm backup backup -archive=/backups/snapshot.tar.gz` stores an archive in the backups volume. Existing files are not overwritten. A backup on the same disk alone is not disaster recovery.

## Restore into a fresh installation

Obtain the same release and Compose files used for the backup. Use a new Compose project (and thus new volumes), not the old database. Do not bootstrap before restoring:

```bash
docker compose -p inventrospect-restored run -T --rm backup restore -archive=- < inventrospect-snapshot.tar.gz
docker compose -p inventrospect-restored up -d
```

Stop the previous installation before starting the restored one on the same ports. Source builds require `docker compose build app` first. Verify login, inventory and document downloads after restore. Restore checks archive hashes, SQLite integrity/foreign keys, and each referenced document's length/checksum. It refuses any nonempty destination (except the lock file) and never merges databases. Interrupted restore markers/staging directories prevent application startup: preserve the failed directory for diagnosis, then retry into another fresh project/volume. Do not delete the original installation until the restored data has been checked. A corrupt archive is rejected before publishing its files.

## Upgrade and downgrade

1. Back up with the **currently running release's** maintenance image, using the stopped procedure above; verify and retain the archive off-host.
2. Record the previous version and configuration. Pin the new published `INVENTROSPECT_IMAGE` tag in `.env`, then `docker compose pull app mcp backup`. For source deployments update the checkout and run `docker compose build app` instead.
3. `docker compose up -d`; check health and `docker compose logs app mcp`. Startup logs report version, migrations and listening endpoints.
4. Check login, representative inventory and document retrieval.

Never downgrade a migrated database in place. Restore a pre-upgrade archive into fresh volumes with its matching older image. Arbitrary reverse migrations are not supported. Keep deployment configuration alongside backups, without plaintext credentials.

## Document reconciliation

Cleanup queue entries are processed independently, retried at startup and hourly, and failures are logged. A failed entry does not block valid later entries; attempts are bounded per pass.

For crash-leftover files, stop both services and run:

```bash
docker compose run --rm backup orphans
# Review the report and take a backup before applying:
docker compose run --rm backup orphans -apply
docker compose up -d
```

This uses the current version's normal migration/cleanup startup, so use the image matching your database. Reconciliation only considers regular, unreferenced files with known storage names older than 24 hours. It ignores recent files, unknown names, directories and symlinks. Dry-run is the default; referenced files are never candidates. Do not edit document storage externally while maintenance runs.

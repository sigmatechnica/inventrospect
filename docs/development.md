# Local container builds

The default Compose configuration pulls the published GHCR image. To build your
checkout instead, use the source-build override. Docker is sufficient; the Go
compiler runs inside the build stage.

From the repository root, in Bash:

```bash
export COMPOSE_FILE=compose.yaml:compose.build.yaml
export INVENTROSPECT_IMAGE=inventrospect:local
docker compose build --pull app
```

These environment variables override the published-image setting in `.env`.
The override supplies the build context and disables registry pulls for the local
image. All three services use the image built by `build app`.

For a fresh data volume, perform the one-time administrator setup from the README,
then run `docker compose up -d`. Skip administrator setup if users already exist.
Keep the two exported variables set for subsequent Compose commands, including
backup/restore. To retain this mode across shells, put both settings in your
untracked `.env` file instead.

After source changes, back up existing data before rebuilding and restarting:

```bash
docker compose build --pull app
docker compose up -d
```

`--pull` refreshes the base images, including the runtime CA bundle. Keep the Go
version in `go.mod` and the Dockerfile build stage updated together. Go security
fixes require rebuilding and replacing the application binaries; updating the
host compiler alone does not patch existing containers. Go 1.27.3 is the minimum
for the eMudhra certificate distrust fix (CVE-2026-111943).

To test the local image with disposable data:

```bash
bash scripts/container-smoke.sh
```

The smoke test uses its own Compose project and temporary volumes. CI uses this
same source-build override so it tests the checkout rather than a published image.
Native Go builds and test commands are listed in the README.

To return to published images, unset these shell variables and remove any local
build overrides from `.env`. Use the published-image setting from `.env.example`,
then follow the documented backup and upgrade procedure before pulling and
starting another version.

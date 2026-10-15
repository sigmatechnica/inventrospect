#!/usr/bin/env bash
set -euo pipefail
version=${1:?Usage: release-build.sh VERSION COMMIT}
commit=${2:?Commit required}
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Expected stable vMAJOR.MINOR.PATCH tag' >&2; exit 1; }
[[ "$commit" =~ ^[0-9a-f]{40}$ ]] || { echo 'Expected full commit SHA' >&2; exit 1; }
build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
mkdir -p dist
for arch in amd64 arm64; do
  dir="dist/inventrospect_${version}_linux_${arch}"
  mkdir -p "$dir"
  for spec in 'inventrospect server' 'inventrospect-mcp mcp' 'inventrospect-ops ops'; do
    read -r binary package <<< "$spec"
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
      -ldflags="-s -w -X inventrospect/internal/buildinfo.Version=$version -X inventrospect/internal/buildinfo.Commit=$commit -X inventrospect/internal/buildinfo.Date=$build_date" \
      -o "$dir/$binary" "./cmd/$package"
  done
  cp LICENSE README.md "$dir/"
  cp -R docs "$dir/"
  tar -C dist -czf "$dir.tar.gz" "$(basename "$dir")"
done
(cd dist && sha256sum "inventrospect_${version}_linux_amd64.tar.gz" "inventrospect_${version}_linux_arm64.tar.gz" > SHA256SUMS)

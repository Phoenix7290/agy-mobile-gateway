#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${REPO_DIR}/dist-releases"
rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

echo "==> [1/3] Building Svelte 5 frontend..."
(cd "${REPO_DIR}/frontend" && npm ci --no-audit --no-fund && npm run build)

echo "==> [2/3] Building multi-OS Go binaries (agy-gateway + agy-cockpit)..."
for target in \
  "linux amd64 agy-gateway-linux-amd64 agy-cockpit-linux-amd64" \
  "linux arm64 agy-gateway-linux-arm64 agy-cockpit-linux-arm64" \
  "darwin amd64 agy-gateway-darwin-amd64 agy-cockpit-darwin-amd64" \
  "darwin arm64 agy-gateway-darwin-arm64 agy-cockpit-darwin-arm64" \
  "windows amd64 agy-gateway-windows-amd64.exe agy-cockpit-windows-amd64.exe" \
  "windows arm64 agy-gateway-windows-arm64.exe agy-cockpit-windows-arm64.exe"
do
  read -r goos goarch gw_name cockpit_name <<<"$target"
  echo "  -> ${goos}/${goarch}"
  (cd "${REPO_DIR}/backend-go" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -ldflags="-s -w" -o "${OUT_DIR}/${gw_name}" ./cmd/agy-gateway)
  (cd "${REPO_DIR}/packages/cockpit" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -ldflags="-s -w" -o "${OUT_DIR}/${cockpit_name}" ./cmd/agy-cockpit)
done

echo "==> [3/3] Creating full bundles (gateway + cockpit)..."
for target in \
  "linux-amd64 agy-gateway-linux-amd64 agy-cockpit-linux-amd64 agy-gateway agy-cockpit tar" \
  "linux-arm64 agy-gateway-linux-arm64 agy-cockpit-linux-arm64 agy-gateway agy-cockpit tar" \
  "darwin-amd64 agy-gateway-darwin-amd64 agy-cockpit-darwin-amd64 agy-gateway agy-cockpit tar" \
  "darwin-arm64 agy-gateway-darwin-arm64 agy-cockpit-darwin-arm64 agy-gateway agy-cockpit tar" \
  "windows-amd64 agy-gateway-windows-amd64.exe agy-cockpit-windows-amd64.exe agy-gateway.exe agy-cockpit.exe zip" \
  "windows-arm64 agy-gateway-windows-arm64.exe agy-cockpit-windows-arm64.exe agy-gateway.exe agy-cockpit.exe zip"
do
  read -r label gw_src cockpit_src gw_dst cockpit_dst fmt <<<"$target"
  stage="$(mktemp -d)"
  cp "${OUT_DIR}/${gw_src}" "${stage}/${gw_dst}"
  cp "${OUT_DIR}/${cockpit_src}" "${stage}/${cockpit_dst}"
  if [[ "$fmt" == "zip" ]]; then
    cp "${REPO_DIR}/backend-go/cmd/agy-gateway/app.ico" "${stage}/agy-gateway.ico"
    (cd "$stage" && zip -q -r "${OUT_DIR}/agy-mobile-gateway-${label}-bundle.zip" .)
  else
    chmod +x "${stage}/${gw_dst}" "${stage}/${cockpit_dst}"
    tar -C "$stage" -czf "${OUT_DIR}/agy-mobile-gateway-${label}-bundle.tar.gz" .
  fi
  rm -rf "$stage"
done

ls -lh "$OUT_DIR"

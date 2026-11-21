#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PREFIX_BIN="${AGY_INSTALL_DIR:-$HOME/.local/bin}"
AUTOSTART="false"
START_NOW="false"
SKIP_BUILD="false"

for arg in "$@"; do
  case "$arg" in
    --autostart) AUTOSTART="true" ;;
    --start) START_NOW="true" ;;
    --skip-build) SKIP_BUILD="true" ;;
    *)
      echo "Uso: $0 [--autostart] [--start] [--skip-build]"
      exit 1
      ;;
  esac
done

mkdir -p "$PREFIX_BIN"

if [[ "$SKIP_BUILD" != "true" ]]; then
  echo "==> [1/4] Compilando frontend Svelte 5..."
  if command -v npm >/dev/null 2>&1; then
    if [[ ! -d "$REPO_DIR/frontend/node_modules" ]]; then
      (cd "$REPO_DIR/frontend" && npm install --no-audit --no-fund)
    fi
    (cd "$REPO_DIR/frontend" && npm run build)
  elif [[ -f "$REPO_DIR/backend-go/internal/web/static/dist/index.html" ]]; then
    echo "    Aviso: npm nao encontrado; usando bundle web pre-compilado do repositorio."
  else
    echo "Erro: npm nao encontrado e bundle estatico ausente." >&2
    exit 1
  fi

  echo "==> [2/4] Compilando agy-gateway e agy-cockpit..."
  (cd "$REPO_DIR/backend-go" && CGO_ENABLED=0 go build -ldflags="-s -w" -o agy-gateway ./cmd/agy-gateway)
  if [[ -d "$REPO_DIR/packages/cockpit" ]]; then
    (cd "$REPO_DIR/packages/cockpit" && CGO_ENABLED=0 go build -ldflags="-s -w" -o agy-cockpit ./cmd/agy-cockpit)
  fi
fi

echo "==> [3/4] Instalando binarios e .env em $PREFIX_BIN..."
cp -f "$REPO_DIR/backend-go/agy-gateway" "$PREFIX_BIN/agy-gateway"
chmod +x "$PREFIX_BIN/agy-gateway"
if [[ -f "$REPO_DIR/packages/cockpit/agy-cockpit" ]]; then
  cp -f "$REPO_DIR/packages/cockpit/agy-cockpit" "$PREFIX_BIN/agy-cockpit"
  chmod +x "$PREFIX_BIN/agy-cockpit"
fi

OS_NAME="$(uname -s)"
if [[ "$OS_NAME" == "Darwin" ]]; then
  CFG_DIR="$HOME/Library/Application Support/agy-gateway"
else
  CFG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/agy-gateway"
fi
mkdir -p "$CFG_DIR"
ENV_FILE="$CFG_DIR/.env"

if [[ ! -f "$ENV_FILE" ]]; then
  AGY_RESOLVED="$(command -v agy || echo agy)"
  # Generate random local Cockpit token if openssl or /dev/urandom is available
  COCKPIT_TOK=""
  if command -v openssl >/dev/null 2>&1; then
    COCKPIT_TOK="$(openssl rand -hex 24)"
  elif command -v python3 >/dev/null 2>&1; then
    COCKPIT_TOK="$(python3 -c 'import secrets; print(secrets.token_hex(24))')"
  fi
  cat >"$ENV_FILE" <<EOF
GATEWAY_MODE=standalone
PORT=8088
ENABLE_TERMINAL=true
ENABLE_TELEMETRY=true
ENABLE_COCKPIT=true
ENABLE_OLLAMA=true
COCKPIT_ADDR=127.0.0.1:8099
COCKPIT_URL=http://127.0.0.1:8099
COCKPIT_ALLOW_POWER=false
COCKPIT_TOKEN=$COCKPIT_TOK
TTYD_WS_URL=ws://127.0.0.1:7681/terminal/ws
AGY_BIN=$AGY_RESOLVED
AGY_WORKSPACE=$REPO_DIR
EOF
  chmod 600 "$ENV_FILE"
fi

echo "==> [4/4] Configurando auto-start (opcional)..."
if [[ "$AUTOSTART" == "true" ]]; then
  "$PREFIX_BIN/agy-gateway" --autostart on
else
  "$PREFIX_BIN/agy-gateway" --autostart off >/dev/null 2>&1 || true
fi

if [[ "$START_NOW" == "true" ]]; then
  exec "$PREFIX_BIN/agy-gateway"
fi

echo "==> Instalacao concluida!"
echo "    Execute '$PREFIX_BIN/agy-gateway' para iniciar o servidor no foreground."
echo "    (Opcional: use '--autostart on' para registrar systemd --user, ou '--console' para abrir o painel TUI.)"
echo "    Lembre de garantir $PREFIX_BIN no seu PATH."

# Guia de Instalação Multiplataforma

O **AGY Mobile Gateway** é escrito em **Go** (`backend-go/`, binário único estático com a interface Svelte embarcada e SQLite puro em Go via `modernc.org/sqlite`) — **não requer Python, Node.js ou Go instalados** para quem baixar os executáveis prontos.

Modos de operação suportados (`GATEWAY_MODE`):
- **`standalone`:** Roda diretamente no mesmo PC/notebook Windows, Linux ou macOS onde o `agy` CLI (e/ou Ollama) está instalado.
- **`gateway`:** Roda 24/7 via Docker em um servidor/Raspberry Pi e controla sua workstation principal via SSH, SCP, WebSocket (`ttyd`) e Wake-on-LAN.
- **`ide-bridge`:** Modo experimental de fila em arquivos (`.agy-bridge/inbox/<job_id>.json` -> `outbox/<job_id>.json`) para integração via agente/extensão dentro da IDE.

---

## 0. Download Direto (Binários Prontos — Recomendado)

Baixe o executável pronto na página oficial de **[GitHub Releases (`v3.3.0`)](https://github.com/Phoenix7290/agy-mobile-gateway/releases/latest)**:

| Sistema Operacional | Arquivo na Release |
| :--- | :--- |
| **Windows (64-bit x86_64)** | [`agy-gateway-windows-amd64.exe`](https://github.com/Phoenix7290/agy-mobile-gateway/releases/download/v3.3.0/agy-gateway-windows-amd64.exe) |
| **Linux (PC x86_64)** | [`agy-gateway-linux-amd64`](https://github.com/Phoenix7290/agy-mobile-gateway/releases/download/v3.3.0/agy-gateway-linux-amd64) |
| **Linux (ARM64 / Raspberry Pi)** | [`agy-gateway-linux-arm64`](https://github.com/Phoenix7290/agy-mobile-gateway/releases/download/v3.3.0/agy-gateway-linux-arm64) |
| **macOS (Apple Silicon M1/M2/M3/M4)** | [`agy-gateway-darwin-arm64`](https://github.com/Phoenix7290/agy-mobile-gateway/releases/download/v3.3.0/agy-gateway-darwin-arm64) |

---

## 1. Windows (Executável Pronto ou WSL2)

### Opção A: Executável Nativo (`agy-gateway-windows-amd64.exe`)
1. Certifique-se de que o **`agy` CLI** esteja instalado e autenticado na máquina.
2. Baixe [`agy-gateway-windows-amd64.exe`](https://github.com/Phoenix7290/agy-mobile-gateway/releases/download/v3.3.0/agy-gateway-windows-amd64.exe).
3. No PowerShell, dentro da pasta do seu projeto/workspace:
```powershell
$env:GATEWAY_MODE = "standalone"
.\agy-gateway-windows-amd64.exe
```
4. Acesse no navegador ou celular (mesma rede/Tailscale): `http://<IP-DO-WINDOWS>:8088` (ou `http://localhost:8088`).

- **Dados / SQLite:** `%LOCALAPPDATA%\agy-gateway\data\gateway.db`
- **Inbox / Outbox:** `%USERPROFILE%\Documents\AGY-Gateway\inbox` e `outbox`
- **Sem janelas piscando:** processos filhos do `agy` são iniciados com `CREATE_NO_WINDOW` (`0x08000000`), mantendo tudo invisível em segundo plano.

### Opção B: Via WSL2 (Ubuntu no Windows)
1. Abra seu terminal Ubuntu no WSL2 com o `agy` CLI autenticado dentro do WSL2.
2. Siga os passos da seção **Linux & macOS** abaixo.

---

## 2. Linux & macOS (Binário Pronto ou Compilando do Código)

### Pré-requisitos
- **Google Antigravity CLI (`agy`)** instalado e autenticado (`agy auth login` ou sessão ativa).

### Usando o binário pronto
```bash
# Exemplo Linux x86_64 (ajuste a URL se for linux-arm64 ou darwin-arm64)
curl -L -o agy-gateway https://github.com/Phoenix7290/agy-mobile-gateway/releases/download/v3.3.0/agy-gateway-linux-amd64
chmod +x agy-gateway

GATEWAY_MODE=standalone ./agy-gateway
```

### Ou compilando a partir do código-fonte (requer Go 1.26+)
```bash
git clone https://github.com/Phoenix7290/agy-mobile-gateway.git
cd agy-mobile-gateway/backend-go

go build -ldflags="-s -w" -o agy-gateway ./cmd/agy-gateway
GATEWAY_MODE=standalone ./agy-gateway
```

### Onde ficam os dados?
- **Linux:** `~/.local/share/agy-gateway/` (SQLite) | `~/agy-inbox` e `~/agy-outbox`
- **macOS:** `~/Library/Application Support/agy-gateway/` | `~/agy-inbox` e `~/agy-outbox`

### Acessando do celular em 10 segundos (Quick Tunnel ou Tailscale)
```bash
cloudflared tunnel --url http://localhost:8088
```
Abra o link `https://*.trycloudflare.com` gerado diretamente no navegador do celular (ou acesse direto via IP do Tailscale `http://<tailscale-ip>:8088`).

---

## 3. Para quem usa apenas a IDE Antigravity no Windows (sem usar IA no terminal)

> **Transparência técnica:** O código deste repositório é **100% open source** — qualquer pessoa pode auditar `backend-go/` e `frontend/` para confirmar que todas as conversas e arquivos ficam apenas no banco SQLite local da própria máquina.

Se um colega usa **apenas a interface gráfica da IDE Antigravity no Windows** e não costuma digitar prompts no terminal, há um detalhe arquitetural importante:

**Uma IDE gráfica ociosa na janela não possui um endpoint HTTP nativo esperando prompts externos.** Por isso existem duas formas reais de operar:

### Caminho 1 (Recomendado e pronto hoje): Baixar o `.exe` + ter o `agy` CLI no mesmo Windows da IDE
Mesmo que o colega nunca digite comandos no terminal para trabalhar no dia a dia:
1. Instale o **`agy` CLI** no Windows e faça login com a mesma conta Google usada na IDE Antigravity.
2. Baixe o [`agy-gateway-windows-amd64.exe`](https://github.com/Phoenix7290/agy-mobile-gateway/releases/download/v3.3.0/agy-gateway-windows-amd64.exe) e execute-o com `$env:GATEWAY_MODE = "standalone"` na pasta do workspace aberto na IDE.
3. Pelo celular, o colega envia o pedido na interface web (`http://<IP>:8088`).
4. O gateway dispara o `agy` em segundo plano (`CREATE_NO_WINDOW`, sem abrir janela preta) editando os arquivos do projeto diretamente — **a IDE aberta no Windows reflete as alterações dos arquivos na hora**.

### Caminho 2 (Experimental / Extensível): Modo `GATEWAY_MODE=ide-bridge`
O backend Go também implementa o protocolo de fila local em `backend-go/internal/jobs/ide_bridge.go`:
```powershell
$env:GATEWAY_MODE = "ide-bridge"
$env:IDE_BRIDGE_DIR = "$PWD\.agy-bridge"
$env:IDE_BRIDGE_TIMEOUT_SEC = "300"
.\agy-gateway-windows-amd64.exe
```
Como funciona:
1. Ao enviar um prompt pelo celular, o gateway grava `.agy-bridge/inbox/<job_id>.json` contendo `{ "job_id", "conversation_id", "prompt", "model", "permission_mode", "workspace" }`.
2. Um agente, extensão ou watcher ativo **dentro da IDE** lê o arquivo em `inbox/`, processa no contexto da IDE e grava a resposta em `.agy-bridge/outbox/<job_id>.json` (`{ "response": "...", "conversation_id": "..." }` ou `"error"`).
3. O gateway detecta a resposta em `outbox/` e transmite via SSE para o celular em tempo real.

*(Observação: o Caminho 2 exige que exista um watcher/extensão rodando dentro da IDE para consumir `.agy-bridge/inbox/`; sem esse componente na IDE, o job atinge o timeout configurado.)*

---

## 4. Linux / Raspberry Pi 24/7 (Modo Gateway Docker)

Ideal para manter um servidor pequeno (Raspberry Pi `aarch64` ou `x86_64`) sempre online com Wake-on-LAN, Terminal Web (`ttyd`), Watchdog e Cloudflare Access / Tailscale:

```bash
git clone https://github.com/Phoenix7290/agy-mobile-gateway.git /opt/stacks/agy-gateway
cd /opt/stacks/agy-gateway

mkdir -p ssh data
ssh-keygen -t ed25519 -f ssh/id_ed25519 -N "" -C "agy-gateway"
chmod 600 ssh/id_ed25519
cat ssh/id_ed25519.pub | ssh usuario@ip-do-pc "mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys"

docker compose up -d --build
```
O `Dockerfile` padrão faz *multi-stage build* em Go (`golang:bookworm` -> `debian:bookworm-slim`) mantendo `./data:/data` e `./ssh:/root/.ssh:ro` persistentes fora do container.

---

## 5. Mods Opcionais (Whisper STT, ntfy, ai-memory, Ollama)

Variáveis de ambiente reconhecidas pelo `agy-gateway` (podem ser passadas via ambiente ou arquivo `.env` no mesmo diretório):
- **Whisper (`POST /api/transcribe`):** defina `ENABLE_WHISPER=true` e `WHISPER_URL=http://127.0.0.1:8000` (apontando para um servidor compatível com `/v1/audio/transcriptions`, como `faster-whisper-server` / `speaches`).
- **Ollama Local:** `ENABLE_OLLAMA=true` (padrão) e `OLLAMA_URL=http://127.0.0.1:11434`.
- **Notificações Push (`ntfy`):** `ENABLE_NTFY=true` e `NTFY_TOPIC=<seu-topico>`.
- **Wake-on-LAN / Terminal / Watchdog:** `ENABLE_WOL`, `WOL_MAC`, `ENABLE_TERMINAL`, `ENABLE_WATCHDOG`, `ENABLE_TELEMETRY`.

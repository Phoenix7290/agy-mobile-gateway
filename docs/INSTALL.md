# Guia de Instalação Multiplataforma

O **AGY Mobile Gateway** possui um backend principal em **Go** (`backend-go/`, binário único estático com a interface Svelte embarcada e SQLite puro em Go via `modernc.org/sqlite`) e mantém o backend legado em **Python/FastAPI** (`app/`) como fallback.

Modos de operação suportados (`GATEWAY_MODE`):
- **`standalone`:** Roda diretamente no mesmo PC/notebook Windows, Linux ou macOS onde o `agy` CLI (e/ou Ollama) está instalado.
- **`gateway`:** Roda 24/7 em um servidor/Raspberry Pi e controla sua workstation principal via SSH, SCP, WebSocket (`ttyd`) e Wake-on-LAN.
- **`ide-bridge`:** Modo experimental de fila em arquivos (`.agy-bridge/inbox/<job_id>.json` -> `outbox/<job_id>.json`) para integração via agente/extensão dentro da IDE.

---

## 1. Teste Rápido — Linux & macOS (Go Standalone — Recomendado)

### Pré-requisitos
- **Google Antigravity CLI (`agy`)** instalado e autenticado (`agy auth login` ou sessão já logada na IDE).
- **Go 1.26+** apenas para compilar (ou use um binário já compilado; em runtime não precisa de Go nem Python).

### Compilando e rodando em 30 segundos
```bash
git clone https://github.com/Phoenix7290/agy-mobile-gateway.git
cd agy-mobile-gateway/backend-go

go build -ldflags="-s -w" -o agy-gateway ./cmd/agy-gateway
GATEWAY_MODE=standalone ./agy-gateway
```
Abra `http://localhost:8088` (ou `http://<IP-DA-MAQUINA>:8088` no celular na mesma rede/Tailscale).

> Se preferir gerar um configurador inicial `.env`, você ainda pode rodar `python3 ../setup.py` na raiz do repositório antes de iniciar `./agy-gateway` (o binário Go lê o `.env` automaticamente se as variáveis não estiverem definidas no ambiente).

### Onde ficam os dados?
- **Linux:** `~/.local/share/agy-gateway/` (SQLite) | `~/agy-inbox` e `~/agy-outbox`
- **macOS:** `~/Library/Application Support/agy-gateway/` | `~/agy-inbox` e `~/agy-outbox`

### Acessando do celular em 10 segundos (Quick Tunnel)
```bash
cloudflared tunnel --url http://localhost:8088
```
Abra o link `https://*.trycloudflare.com` gerado diretamente no navegador do celular (ou acesse direto via Tailscale `http://<tailscale-ip>:8088`).

---

## 2. Windows (Nativo em Go ou WSL2)

### Opção A: Binário Nativo Windows (`agy-gateway.exe`, sem Python/WSL)
Você pode compilar no próprio Windows (com Go instalado) ou cruzar-compilar de um PC Linux/macOS sem nenhuma dependência de C/CGO:
```bash
# Cross-compile no Linux/macOS para Windows 64-bit:
cd backend-go
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o agy-gateway.exe ./cmd/agy-gateway
```

No PowerShell do Windows:
```powershell
$env:GATEWAY_MODE = "standalone"
.\agy-gateway.exe
```
- **Dados / SQLite:** `%LOCALAPPDATA%\agy-gateway\data\gateway.db`
- **Inbox / Outbox:** `%USERPROFILE%\Documents\AGY-Gateway\inbox` e `outbox`
- **Sem janelas piscando:** processos filhos do `agy` são iniciados com `CREATE_NO_WINDOW` (`0x08000000`), mantendo o uso invisível em segundo plano.

### Opção B: Via WSL2 (Ubuntu no Windows)
1. Abra seu terminal Ubuntu no WSL2 com o `agy` CLI autenticado dentro do WSL2.
2. Siga exatamente os mesmos passos da seção **Linux & macOS (Go Standalone)** acima.

---

## 3. Para quem usa apenas a IDE Antigravity no Windows (sem usar IA no terminal)

> **Transparência técnica:** O código deste repositório é **100% open source** — qualquer pessoa pode auditar `backend-go/`, `app/` e `frontend/` para confirmar que todas as conversas e arquivos ficam apenas no banco SQLite local da própria máquina.

Se um colega usa **apenas a interface gráfica da IDE Antigravity no Windows** e não costuma digitar prompts no terminal, há um detalhe arquitetural importante:

**Uma IDE gráfica ociosa na janela não possui um endpoint HTTP nativo esperando prompts externos.** Por isso existem duas formas reais de operar:

### Caminho 1 (Recomendado e pronto hoje): Instalar o `agy` CLI no mesmo Windows da IDE
Mesmo que o colega nunca digite comandos no terminal para trabalhar no dia a dia:
1. Instale o **`agy` CLI** no Windows e faça login com a mesma conta Google usada na IDE Antigravity (ou compartilhe as credenciais locais).
2. Baixe ou rode `agy-gateway.exe` com `GATEWAY_MODE=standalone` dentro da mesma pasta de projeto que a IDE já usa (`WORKDIR` ou workspace).
3. Pelo celular, o colega envia o pedido na interface web (`http://<IP>:8088`).
4. O `agy-gateway.exe` dispara o `agy` em segundo plano (`CREATE_NO_WINDOW`, sem abrir janela preta) editando os arquivos do projeto diretamente — **a IDE aberta no Windows reflete as alterações dos arquivos e o histórico na hora**.

### Caminho 2 (Experimental / Extensível): Modo `GATEWAY_MODE=ide-bridge`
O backend Go já implementa o protocolo de fila local em `backend-go/internal/jobs/ide_bridge.go`:
```powershell
$env:GATEWAY_MODE = "ide-bridge"
$env:IDE_BRIDGE_DIR = "$PWD\.agy-bridge"
$env:IDE_BRIDGE_TIMEOUT_SEC = "300"
.\agy-gateway.exe
```
Como funciona:
1. Ao enviar um prompt pelo celular, o gateway grava `.agy-bridge/inbox/<job_id>.json` contendo `{ "job_id", "conversation_id", "prompt", "model", "permission_mode", "workspace" }`.
2. Um agente, extensão ou watcher ativo **dentro da IDE** lê o arquivo em `inbox/`, processa no contexto da IDE e grava a resposta em `.agy-bridge/outbox/<job_id>.json` (`{ "response": "...", "conversation_id": "..." }` ou `"error"`).
3. O gateway detecta a resposta em `outbox/` e transmite via SSE para o celular em tempo real.

*(Observação: o Caminho 2 exige que exista um watcher/extensão rodando dentro da IDE para consumir `.agy-bridge/inbox/`; sem esse componente na IDE, o job atinge o timeout configurado.)*

---

## 4. Linux / Raspberry Pi 24/7 (Modo Gateway Docker em Go)

Ideal para manter um servidor pequeno (Raspberry Pi `aarch64` ou x86_64) sempre online com Wake-on-LAN, Terminal Web (`ttyd`), Watchdog e Cloudflare Access / Tailscale:

```bash
git clone https://github.com/Phoenix7290/agy-mobile-gateway.git /opt/stacks/agy-gateway
cd /opt/stacks/agy-gateway

mkdir -p ssh data
ssh-keygen -t ed25519 -f ssh/id_ed25519 -N "" -C "agy-gateway"
chmod 600 ssh/id_ed25519
cat ssh/id_ed25519.pub | ssh usuario@ip-do-pc "mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys"

docker compose up -d --build
```
O `Dockerfile` padrão faz *multi-stage build* em Go (`golang:bookworm` -> `debian:bookworm-slim`) sem precisar manter `app/` montado em volume e mantendo `./data:/data` compatível 1:1 com o banco SQLite existente.

---

## 5. Mods Opcionais (Whisper STT, ntfy, ai-memory, Ollama)

Variáveis de ambiente reconhecidas pelo `agy-gateway`:
- **Whisper (`POST /api/transcribe`):** defina `ENABLE_WHISPER=true` e `WHISPER_URL=http://127.0.0.1:8000` (apontando para um servidor compatível com `/v1/audio/transcriptions`, como `faster-whisper-server` / `speaches`).
- **Ollama Local:** `ENABLE_OLLAMA=true` (padrão) e `OLLAMA_URL=http://127.0.0.1:11434`.
- **Notificações Push (`ntfy`):** `ENABLE_NTFY=true` e `NTFY_TOPIC=<seu-topico>`.
- **Wake-on-LAN / Terminal / Watchdog:** `ENABLE_WOL`, `WOL_MAC`, `ENABLE_TERMINAL`, `ENABLE_WATCHDOG`, `ENABLE_TELEMETRY`.

---

## 6. Fallback Legado em Python (FastAPI)

Se você precisar rodar especificamente o backend Python (`app/main.py`):

### Local (Linux / macOS / Windows)
```bash
python3 setup.py
python3 -m venv .venv
source .venv/bin/activate   # No Windows: .\.venv\Scripts\Activate.ps1
pip install -r requirements.txt
uvicorn --app-dir app main:app --host 0.0.0.0 --port 8088
```

### Docker Python
O Dockerfile antigo foi preservado em `Dockerfile.python`:
```bash
docker build -f Dockerfile.python -t agy-gateway-python .
```

# Guia de Instalação Multiplataforma

O **AGY Mobile Gateway** é escrito em **Go** (`backend-go/`, binário único estático com a interface Svelte embarcada e SQLite puro em Go via `modernc.org/sqlite`) — **não requer Python, Node.js ou Go instalados** para quem baixar os executáveis prontos.

Modos de operação suportados (`GATEWAY_MODE`):
- **`standalone`:** Roda diretamente no mesmo PC/notebook Windows, Linux ou macOS onde o `agy` CLI (e/ou Ollama) está instalado.
- **`gateway`:** Roda 24/7 via Docker em um servidor/Raspberry Pi e controla sua workstation principal via SSH, SCP, WebSocket (`ttyd`) e Wake-on-LAN.
- **`ide-bridge`:** Modo experimental de fila em arquivos (`.agy-bridge/inbox/<job_id>.json` -> `outbox/<job_id>.json`) para integração via agente/extensão dentro da IDE.

---

## 0. Download Direto (Bundles & Binários Prontos — Recomendado)

Baixe o pacote pronto para o seu sistema operacional na página oficial de **[GitHub Releases](https://github.com/Phoenix7290/agy-mobile-gateway/releases/latest)**:

| Sistema Operacional | Arquivo na Release |
| :--- | :--- |
| **Windows (x86_64 bundle: Gateway + Cockpit)** | `agy-mobile-gateway-windows-amd64-bundle.zip` (ou `agy-gateway-windows-amd64.exe`) |
| **Linux (PC x86_64 bundle)** | `agy-mobile-gateway-linux-amd64-bundle.tar.gz` (ou `agy-gateway-linux-amd64`) |
| **Linux (ARM64 / Raspberry Pi bundle)** | `agy-mobile-gateway-linux-arm64-bundle.tar.gz` (ou `agy-gateway-linux-arm64`) |
| **macOS (Apple Silicon / Intel bundle)** | `agy-mobile-gateway-darwin-arm64-bundle.tar.gz` / `darwin-amd64` |

Ao executar o `agy-gateway` interativamente no **Windows** (ou passando `--console` no Linux/macOS):
- Abre o **Painel de Controle no Terminal** (`[1]` Ligar, `[2]` Desligar/Privacidade, `[4]` Abrir Navegador, `[7]` Ligar/Desligar Auto-Start no boot, `[8]` Criar Atalho no Menu/Desktop, `[Q]` Fechar encerrando tudo);
- No Linux/macOS sem flags, `agy-gateway` mantém o comportamento padrão Unix (servidor HTTP direto no foreground com logs no `stdout`), ideal para `systemd`, `tmux` ou Docker Compose;
- Na primeira execução sem `.env`, cria automaticamente um `.env` padrão seguro (`COCKPIT_ALLOW_POWER=false` e `COCKPIT_TOKEN` gerado localmente) em `%LOCALAPPDATA%\agy-gateway\.env` (Windows), `~/Library/Application Support/agy-gateway/.env` (macOS) ou `~/.config/agy-gateway/.env` (Linux).

---

## 1. Windows (Executável Pronto, Instalador ou WSL2)

### Opção A: Instalador automático (a partir do código clonado)
```powershell
.\scripts\install-windows.ps1            # Instala, cria atalhos Menu Iniciar + Desktop (inicia apenas ao clicar)
# ou com auto-start no logon:
.\scripts\install-windows.ps1 -AutoStart
```

### Opção B: Executável/Bundle Pronto (`agy-gateway.exe`)
1. Certifique-se de que o **`agy` CLI** esteja instalado e autenticado na máquina.
2. Baixe o `agy-mobile-gateway-windows-amd64-bundle.zip` (extraia em uma pasta) e execute `agy-gateway.exe`.
3. O painel interativo abre no terminal exibindo os links do PC e do celular na rede (`http://<IP-LOCAL>:8088`), com teclas `[7]` para configurar Auto-Start no boot e `[8]` para criar atalho na Área de Trabalho/Menu Iniciar.

- **Dados / SQLite:** `%LOCALAPPDATA%\agy-gateway\data\gateway.db`
- **Config `.env`:** `%LOCALAPPDATA%\agy-gateway\.env`
- **Inbox / Outbox:** `%USERPROFILE%\Documents\AGY-Gateway\inbox` e `outbox`
- **Privacidade / Zero janelas extras:** processos filhos usam Job Object (`JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`) e `CREATE_NO_WINDOW` — fechar a janela do painel encerra tudo automaticamente.

### Opção C: Via WSL2 (Ubuntu no Windows)
1. Abra seu terminal Ubuntu no WSL2 com o `agy` CLI autenticado dentro do WSL2.
2. Siga os passos da seção **Linux & macOS** abaixo.

---

## 2. Linux & macOS (Instalador, Binário Pronto ou Compilando do Código)

### Pré-requisitos
- **Google Antigravity CLI (`agy`)** instalado e autenticado (`agy auth login` ou sessão ativa).

### Instalador automático (a partir do código clonado)
```bash
chmod +x scripts/install.sh
./scripts/install.sh              # Instala em ~/.local/bin e cria atalho de app (.desktop / .command)
# ou com auto-start (systemd --user no Linux / LaunchAgent no macOS):
./scripts/install.sh --autostart
```

### Usando o binário/bundle pronto
```bash
# Extraia o bundle tar.gz da Release e execute no terminal:
./agy-gateway
```
O mesmo painel interativo funciona no Linux e macOS (`termios`), permitindo alternar **`[7]` Auto-Start no boot** (`systemd --user` / `autostart` no Linux ou `LaunchAgent` no macOS) e **`[8]` Criar atalho de App**. Para rodar como servidor puro sem painel interativo (ex.: Docker/SSH), use `./agy-gateway --daemon` (ou `AGY_HEADLESS=1`).

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
- **Cockpit (`packages/cockpit`, opcional fora do Core):** painel vivo com gráficos SVG (VRAM, GPU Temp, CPU, RAM), top processos, controle Docker (`start/stop/logs`) e ações de energia protegidas. Rode `packages/cockpit/cmd/agy-cockpit` na workstation e habilite no Gateway com `ENABLE_COCKPIT=true` + `COCKPIT_URL=http://127.0.0.1:8099` (ou `--profile cockpit` no Docker Compose). Veja [packages/cockpit/README.md](../packages/cockpit/README.md).

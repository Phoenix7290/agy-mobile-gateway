# Guia de Instalação Multiplataforma

O **AGY Mobile Gateway** suporta dois modos de operação:
- **Standalone:** Roda diretamente no mesmo PC/notebook onde o `agy` CLI está instalado (ideal para colegas testarem rapidamente).
- **Gateway Remoto:** Roda 24/7 em um servidor/Raspberry Pi e controla sua workstation principal via SSH, SCP e Wake-on-LAN.

---

## 1. Teste Rápido — Linux & macOS (Standalone)

### Pré-requisitos
- **Google Antigravity CLI (`agy`)** instalado e autenticado no terminal.
- **Python 3.11+**

### Passos
```bash
git clone https://github.com/Phoenix7290/agy-mobile-gateway.git
cd agy-mobile-gateway

python3 setup.py   # Escolha opção 1 (Standalone) e selecione seus Mods opcionais

python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt

uvicorn --app-dir app main:app --host 0.0.0.0 --port 8088
```

### Onde ficam os dados?
- **Linux:** `~/.local/share/agy-gateway/` (SQLite) | `~/agy-inbox` e `~/agy-outbox`
- **macOS:** `~/Library/Application Support/agy-gateway/` | `~/agy-inbox` e `~/agy-outbox`

### Acessando do celular em 10 segundos (Quick Tunnel)
Se não quiser configurar VPN nem domínio ainda:
```bash
cloudflared tunnel --url http://localhost:8088
```
Abra o link `https://*.trycloudflare.com` gerado diretamente no navegador do celular.

---

## 2. Windows (WSL2 ou Nativo)

### Opção A: Via WSL2 (Recomendado)
1. Abra seu terminal Ubuntu no WSL2.
2. Siga exatamente os mesmos passos da seção **Linux (Standalone)** acima.

### Opção B: Windows Nativo (PowerShell)
```powershell
git clone https://github.com/Phoenix7290/agy-mobile-gateway.git
cd agy-mobile-gateway

python setup.py    # Escolha opção 1 (Standalone)

python -m venv .venv
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt

uvicorn --app-dir app main:app --host 0.0.0.0 --port 8088
```
- **Dados / SQLite:** `%LOCALAPPDATA%\agy-gateway\data\`
- **Inbox / Outbox:** `%USERPROFILE%\Documents\AGY-Gateway\inbox` e `outbox`
- Processos do `agy` são iniciados com `CREATE_NO_WINDOW` para não abrir janelas de prompt piscando.

---

## 3. Linux / Raspberry Pi 24/7 (Modo Gateway Docker)

Ideal para manter um servidor pequeno (Raspberry Pi) sempre online com Wake-on-LAN, Terminal Web (`ttyd`) e Cloudflare Access 2FA:

```bash
git clone https://github.com/Phoenix7290/agy-mobile-gateway.git /opt/stacks/agy-gateway
cd /opt/stacks/agy-gateway

mkdir -p ssh data
ssh-keygen -t ed25519 -f ssh/id_ed25519 -N "" -C "agy-gateway"
chmod 600 ssh/id_ed25519
cat ssh/id_ed25519.pub | ssh usuario@ip-do-pc "mkdir -p ~/.ssh && cat >> ~/.ssh/authorized_keys"

python3 setup.py   # Escolha opção 2 (Gateway Remoto)
docker compose up -d --build
```

#!/usr/bin/env python3
"""Interactive Setup Wizard for AGY Mobile Gateway (Linux, macOS, Windows)."""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "app"))
from config import get_app_dirs  # noqa: E402


def ask_yn(prompt: str, default: bool = True) -> bool:
    hint = "S/n" if default else "s/N"
    ans = input(f"{prompt} [{hint}]: ").strip().lower()
    if not ans:
        return default
    return ans in ("s", "sim", "y", "yes", "1")


def ask_str(prompt: str, default: str = "") -> str:
    hint = f" [{default}]" if default else ""
    ans = input(f"{prompt}{hint}: ").strip()
    return ans or default


def main() -> None:
    print("=== AGY Mobile Gateway — Setup Wizard ===\n")
    print("Escolha o modo de operação:")
    print("  1) Standalone (roda direto neste PC — recomendado para colegas/notebooks)")
    print("  2) Gateway Remoto (ex: Raspberry Pi controlando um PC via SSH/WoL)")
    mode_choice = ask_str("Opção (1 ou 2)", "1")
    mode = "gateway" if mode_choice == "2" else "standalone"

    dirs = get_app_dirs()
    os.makedirs(dirs.data_dir, exist_ok=True)
    os.makedirs(dirs.cache_dir, exist_ok=True)
    os.makedirs(dirs.inbox_dir, exist_ok=True)
    os.makedirs(dirs.outbox_dir, exist_ok=True)

    env_lines = [
        f"GATEWAY_MODE={mode}",
        f"PORT={ask_str('Porta HTTP do Gateway', '8088')}",
    ]

    if mode == "gateway":
        env_lines.append(f"TARGET_HOST={ask_str('IP/Host do PC alvo', '192.168.1.190')}")
        env_lines.append(f"TARGET_USER={ask_str('Usuário SSH no PC alvo', 'ai')}")
        env_lines.append(f"SSH_KEY={ask_str('Caminho da chave SSH privada', '/root/.ssh/id_ed25519')}")
        use_wol = ask_yn("Ativar Wake-on-LAN (WoL)?", True)
        env_lines.append(f"ENABLE_WOL={'true' if use_wol else 'false'}")
        if use_wol:
            env_lines.append(f"WOL_MAC={ask_str('MAC Address do PC alvo', 'bc:fc:e7:ca:84:05')}")
        env_lines.append(f"ENABLE_WATCHDOG={'true' if ask_yn('Ativar Watchdog de disponibilidade?', True) else 'false'}")
        env_lines.append(f"ENABLE_TERMINAL={'true' if ask_yn('Ativar Terminal Web (ttyd)?', True) else 'false'}")
    else:
        env_lines.append("ENABLE_WOL=false")
        env_lines.append("ENABLE_WATCHDOG=false")
        env_lines.append("ENABLE_TERMINAL=false")

    print("\n--- Mods Opcionais ---")
    use_ollama = ask_yn("Ativar integração com Ollama local?", True)
    env_lines.append(f"ENABLE_OLLAMA={'true' if use_ollama else 'false'}")
    if use_ollama:
        env_lines.append(f"OLLAMA_URL={ask_str('URL do Ollama', 'http://127.0.0.1:11434')}")

    use_aimem = ask_yn("Conectar ao servidor ai-memory?", False)
    env_lines.append(f"ENABLE_AI_MEMORY={'true' if use_aimem else 'false'}")
    if use_aimem:
        env_lines.append(f"AI_MEMORY_SERVER_URL={ask_str('URL do ai-memory', 'http://127.0.0.1:49374')}")

    use_ntfy = ask_yn("Ativar notificações Push via ntfy?", False)
    env_lines.append(f"ENABLE_NTFY={'true' if use_ntfy else 'false'}")
    if use_ntfy:
        env_lines.append(f"NTFY_URL={ask_str('URL do servidor ntfy', 'https://ntfy.sh')}")
        env_lines.append(f"NTFY_TOPIC={ask_str('Nome do tópico privado ntfy', 'agy-mobile-alerts')}")

    env_path = os.path.join(os.path.dirname(__file__), ".env")
    with open(env_path, "w", encoding="utf-8") as f:
        f.write("\n".join(env_lines) + "\n")

    print("\nConfiguração concluída com sucesso!")
    print(f"  - Arquivo gerado: {env_path}")
    print(f"  - Dados / SQLite: {dirs.data_dir}")
    print(f"  - Inbox:          {dirs.inbox_dir}")
    print(f"  - Outbox:         {dirs.outbox_dir}")
    if mode == "standalone":
        print("\nPara iniciar localmente:")
        print("  pip install -r requirements.txt")
        print("  uvicorn --app-dir app main:app --host 0.0.0.0 --port 8088")
        print("\nPara testar no celular rapidamente sem configurar roteador:")
        print("  cloudflared tunnel --url http://localhost:8088")
    else:
        print("\nPara iniciar via Docker:")
        print("  docker compose up -d --build")


if __name__ == "__main__":
    main()

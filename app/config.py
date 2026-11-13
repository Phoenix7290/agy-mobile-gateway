import os
import sys
from dataclasses import dataclass
from typing import Dict, Any


def _bool_env(name: str, default: bool) -> bool:
    val = os.getenv(name)
    if val is None or val.strip() == "":
        return default
    return val.strip().lower() in ("1", "true", "yes", "on")


@dataclass(frozen=True)
class AppDirs:
    data_dir: str
    cache_dir: str
    inbox_dir: str
    outbox_dir: str


def get_app_dirs() -> AppDirs:
    """Resolve OS-appropriate directories without polluting user root."""
    in_docker = os.path.exists("/.dockerenv")
    home = os.path.expanduser("~")

    if in_docker:
        data_dir = os.getenv("DATA_DIR", "/data")
        cache_dir = os.getenv("CACHE_DIR", os.path.join(data_dir, "cache"))
        inbox_dir = os.getenv("INBOX_DIR", "/home/ai/agy-inbox")
        outbox_dir = os.getenv("OUTBOX_DIR", "/home/ai/agy-outbox")
    elif sys.platform == "win32":
        local_app = os.getenv("LOCALAPPDATA") or os.path.join(home, "AppData", "Local")
        data_dir = os.getenv("DATA_DIR", os.path.join(local_app, "agy-gateway", "data"))
        cache_dir = os.getenv("CACHE_DIR", os.path.join(local_app, "agy-gateway", "cache"))
        docs_base = os.path.join(home, "Documents", "AGY-Gateway")
        inbox_dir = os.getenv("INBOX_DIR", os.path.join(docs_base, "inbox"))
        outbox_dir = os.getenv("OUTBOX_DIR", os.path.join(docs_base, "outbox"))
    elif sys.platform == "darwin":
        app_support = os.path.join(home, "Library", "Application Support", "agy-gateway")
        data_dir = os.getenv("DATA_DIR", os.path.join(app_support, "data"))
        cache_dir = os.getenv("CACHE_DIR", os.path.join(home, "Library", "Caches", "agy-gateway"))
        inbox_dir = os.getenv("INBOX_DIR", os.path.join(home, "agy-inbox"))
        outbox_dir = os.getenv("OUTBOX_DIR", os.path.join(home, "agy-outbox"))
    else:
        xdg_data = os.getenv("XDG_DATA_HOME", os.path.join(home, ".local", "share"))
        xdg_cache = os.getenv("XDG_CACHE_HOME", os.path.join(home, ".cache"))
        data_dir = os.getenv("DATA_DIR", os.path.join(xdg_data, "agy-gateway"))
        cache_dir = os.getenv("CACHE_DIR", os.path.join(xdg_cache, "agy-gateway"))
        inbox_dir = os.getenv("INBOX_DIR", os.path.join(home, "agy-inbox"))
        outbox_dir = os.getenv("OUTBOX_DIR", os.path.join(home, "agy-outbox"))

    return AppDirs(
        data_dir=data_dir,
        cache_dir=cache_dir,
        inbox_dir=inbox_dir,
        outbox_dir=outbox_dir,
    )


@dataclass(frozen=True)
class GatewaySettings:
    mode: str  # "gateway" (SSH/Pi) or "standalone" (local)
    target_host: str
    target_user: str
    wol_mac: str
    ssh_key: str
    ollama_url: str
    ttyd_ws_url: str
    dirs: AppDirs
    mods: Dict[str, Any]


def load_config() -> GatewaySettings:
    in_docker = os.path.exists("/.dockerenv")
    env_mode = os.getenv("GATEWAY_MODE", "").strip().lower()
    if env_mode in ("gateway", "standalone"):
        mode = env_mode
    elif in_docker or os.getenv("TARGET_HOST"):
        mode = "gateway"
    else:
        mode = "standalone"

    dirs = get_app_dirs()
    is_gateway = mode == "gateway"

    wol_mac = os.getenv("WOL_MAC", "bc:fc:e7:ca:84:05" if is_gateway else "")
    enable_wol = _bool_env("ENABLE_WOL", is_gateway and bool(wol_mac))
    enable_terminal = _bool_env("ENABLE_TERMINAL", is_gateway)
    enable_watchdog = _bool_env("ENABLE_WATCHDOG", is_gateway)
    enable_ollama = _bool_env("ENABLE_OLLAMA", True)

    mods: Dict[str, Any] = {
        "terminal": enable_terminal,
        "wol": enable_wol,
        "watchdog": enable_watchdog,
        "ollama": enable_ollama,
        "inbox_outbox": True,
        "fts5_search": True,
        "ai_memory": _bool_env("ENABLE_AI_MEMORY", False) or bool(os.getenv("AI_MEMORY_SERVER_URL")),
        "ntfy": _bool_env("ENABLE_NTFY", False) or bool(os.getenv("NTFY_TOPIC")),
        "whisper": _bool_env("ENABLE_WHISPER", False),
        "telemetry": _bool_env("ENABLE_TELEMETRY", is_gateway),
        "slash_commands": ["/compact", "/usage", "/diff", "/mem", "/plan", "/clear", "/status"],
    }

    return GatewaySettings(
        mode=mode,
        target_host=os.getenv("TARGET_HOST", "192.168.1.190" if is_gateway else "127.0.0.1"),
        target_user=os.getenv("TARGET_USER", "ai" if is_gateway else os.getenv("USER", os.getenv("USERNAME", "local"))),
        wol_mac=wol_mac,
        ssh_key=os.getenv("SSH_KEY", "/root/.ssh/id_ed25519"),
        ollama_url=os.getenv("OLLAMA_URL", "http://127.0.0.1:11434"),
        ttyd_ws_url=os.getenv("TTYD_WS_URL", "ws://127.0.0.1:7681/terminal/ws"),
        dirs=dirs,
        mods=mods,
    )

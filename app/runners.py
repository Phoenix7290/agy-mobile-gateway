import os
import sys
import json
import glob
import shlex
import socket
import asyncio
import urllib.request
from datetime import datetime
from typing import Optional, List, Dict, Any, AsyncIterator

from config import GatewaySettings


def _win_creation_flags() -> int:
    if sys.platform == "win32":
        return getattr(asyncio.subprocess, "CREATE_NO_WINDOW", 0x08000000)
    return 0


class BaseRunner:
    """Abstract execution runner for AGY CLI, Ollama, Inbox/Outbox, and Session Sync."""

    def __init__(self, cfg: GatewaySettings):
        self.cfg = cfg

    async def check_online(self, timeout: float = 1.5) -> bool:
        raise NotImplementedError

    async def start_agy_process(self, agy_args: List[str]) -> asyncio.subprocess.Process:
        raise NotImplementedError

    async def start_ollama_chat(self, payload_json: str) -> asyncio.subprocess.Process:
        raise NotImplementedError

    async def list_ollama_models(self) -> List[Dict[str, str]]:
        raise NotImplementedError

    async def list_agy_conversations(self, limit: int = 100) -> List[Dict[str, Any]]:
        raise NotImplementedError

    async def get_agy_conversation(self, conv_id: str) -> Optional[Dict[str, Any]]:
        raise NotImplementedError

    async def save_to_inbox(self, local_tmp_path: str, clean_name: str) -> str:
        raise NotImplementedError

    async def list_outbox(self) -> List[Dict[str, Any]]:
        raise NotImplementedError

    async def stream_outbox_file(self, filename: str) -> AsyncIterator[bytes]:
        raise NotImplementedError
        yield b""

    async def delete_outbox_file(self, filename: str) -> None:
        raise NotImplementedError

    async def get_telemetry(self) -> Dict[str, Any]:
        raise NotImplementedError


class SSHRunner(BaseRunner):
    """Gateway mode: executes AGY/Ollama/Inbox/Outbox on a remote workstation over SSH/SCP."""

    def _ssh_base(self, timeout: float = 5.0) -> List[str]:
        return [
            "ssh", "-i", self.cfg.ssh_key,
            "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
            "-o", "BatchMode=yes", "-o", f"ConnectTimeout={int(timeout)}",
            f"{self.cfg.target_user}@{self.cfg.target_host}",
        ]

    async def _run_ssh(self, cmd_str: str, timeout: float = 5.0) -> Optional[str]:
        proc = await asyncio.create_subprocess_exec(
            *self._ssh_base(timeout), cmd_str,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
        )
        try:
            stdout, _ = await asyncio.wait_for(proc.communicate(), timeout=timeout + 2.0)
            if proc.returncode == 0:
                return stdout.decode("utf-8", errors="replace").strip()
        except asyncio.TimeoutError:
            proc.kill()
        return None

    async def check_online(self, timeout: float = 1.5) -> bool:
        def _probe() -> bool:
            try:
                with socket.create_connection((self.cfg.target_host, 22), timeout=timeout):
                    return True
            except OSError:
                return False
        return await asyncio.to_thread(_probe)

    async def start_agy_process(self, agy_args: List[str]) -> asyncio.subprocess.Process:
        agy_cmd = " ".join(shlex.quote(a) for a in agy_args)
        return await asyncio.create_subprocess_exec(
            *self._ssh_base(5.0), agy_cmd,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
        )

    async def start_ollama_chat(self, payload_json: str) -> asyncio.subprocess.Process:
        remote_cmd = f"curl -s -N -X POST {shlex.quote(self.cfg.ollama_url + '/api/chat')} -d {shlex.quote(payload_json)}"
        return await asyncio.create_subprocess_exec(
            *self._ssh_base(5.0), remote_cmd,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
        )

    async def list_ollama_models(self) -> List[Dict[str, str]]:
        out: List[Dict[str, str]] = []
        stdout = await self._run_ssh(f"curl -s --max-time 2 {shlex.quote(self.cfg.ollama_url + '/api/tags')}", timeout=3.0)
        if stdout:
            try:
                for m in json.loads(stdout).get("models", []):
                    m_name = m.get("name")
                    if m_name and not m_name.startswith("nomic"):
                        out.append({"id": f"ollama:{m_name}", "name": f"Local: {m_name}", "type": "local", "badge": "Ollama / GPU"})
            except Exception:
                pass
        return out

    async def list_agy_conversations(self, limit: int = 100) -> List[Dict[str, Any]]:
        stdout = await self._run_ssh(f"python3 /home/ai/bin/agy-gateway-sync.py list {int(limit)}", timeout=6.0)
        if stdout:
            try:
                return json.loads(stdout).get("conversations", [])
            except Exception:
                pass
        return []

    async def get_agy_conversation(self, conv_id: str) -> Optional[Dict[str, Any]]:
        stdout = await self._run_ssh(f"python3 /home/ai/bin/agy-gateway-sync.py get {shlex.quote(conv_id)}", timeout=5.0)
        if stdout:
            try:
                data = json.loads(stdout)
                if "messages" in data:
                    return data
            except Exception:
                pass
        return None

    async def save_to_inbox(self, local_tmp_path: str, clean_name: str) -> str:
        remote_path = f"{self.cfg.dirs.inbox_dir}/{clean_name}"
        scp_cmd = [
            "scp", "-i", self.cfg.ssh_key,
            "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
            "-o", "BatchMode=yes",
            local_tmp_path,
            f"{self.cfg.target_user}@{self.cfg.target_host}:{remote_path}",
        ]
        proc = await asyncio.create_subprocess_exec(*scp_cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
        _, stderr = await proc.communicate()
        if proc.returncode != 0:
            raise RuntimeError(f"SCP falhou: {stderr.decode('utf-8', errors='replace')}")
        return remote_path

    async def list_outbox(self) -> List[Dict[str, Any]]:
        py_cmd = (
            f"python3 -c 'import os,json; p={shlex.quote(self.cfg.dirs.outbox_dir)}; os.makedirs(p,exist_ok=True); "
            "print(json.dumps([{\"name\":f,\"size\":os.path.getsize(os.path.join(p,f)),\"mtime\":os.path.getmtime(os.path.join(p,f))} "
            "for f in sorted(os.listdir(p)) if os.path.isfile(os.path.join(p,f))]))'"
        )
        stdout = await self._run_ssh(py_cmd, timeout=4.0)
        return json.loads(stdout) if stdout else []

    async def stream_outbox_file(self, filename: str) -> AsyncIterator[bytes]:
        remote_path = f"{self.cfg.dirs.outbox_dir}/{filename}"
        proc = await asyncio.create_subprocess_exec(
            *self._ssh_base(5.0), f"cat {shlex.quote(remote_path)}",
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
        )
        while True:
            chunk = await proc.stdout.read(65536)  # type: ignore
            if not chunk:
                break
            yield chunk
        await proc.wait()

    async def delete_outbox_file(self, filename: str) -> None:
        remote_path = f"{self.cfg.dirs.outbox_dir}/{filename}"
        await self._run_ssh(f"rm -f {shlex.quote(remote_path)}", timeout=4.0)

    async def get_telemetry(self) -> Dict[str, Any]:
        py_cmd = (
            "python3 -c 'import json,subprocess,glob;"
            "res={\"gpu\":None,\"cpu_temp\":None,\"ram\":None};"
            "\ntry:\n"
            " o=subprocess.check_output([\"nvidia-smi\",\"--query-gpu=name,memory.used,memory.total,temperature.gpu\",\"--format=csv,noheader,nounits\"],timeout=2).decode().strip().split(\", \")\n"
            " res[\"gpu\"]={\"name\":o[0],\"vram_used_mb\":round(float(o[1])),\"vram_total_mb\":round(float(o[2])),\"temp_c\":round(float(o[3]))}\n"
            "except Exception: pass\n"
            "try:\n"
            " m={k:int(v.split()[0]) for k,v in (l.split(\":\") for l in open(\"/proc/meminfo\") if \":\" in l)}\n"
            " tot=m.get(\"MemTotal\",0)//1024; avail=m.get(\"MemAvailable\",0)//1024\n"
            " res[\"ram\"]={\"used_mb\":tot-avail,\"total_mb\":tot}\n"
            "except Exception: pass\n"
            "try:\n"
            " temps=[int(open(p).read().strip())/1000 for p in glob.glob(\"/sys/class/hwmon/hwmon*/temp1_input\")[:4]]\n"
            " if temps: res[\"cpu_temp\"]=round(max(temps),1)\n"
            "except Exception: pass\n"
            "print(json.dumps(res))'"
        )
        stdout = await self._run_ssh(py_cmd, timeout=4.0)
        if stdout:
            try:
                return json.loads(stdout)
            except Exception:
                pass
        return {"gpu": None, "cpu_temp": None, "ram": None}


class LocalRunner(BaseRunner):
    """Standalone mode: executes AGY CLI, Ollama, and Inbox/Outbox directly on localhost (Linux/macOS/Windows)."""

    def __init__(self, cfg: GatewaySettings):
        super().__init__(cfg)
        os.makedirs(cfg.dirs.inbox_dir, exist_ok=True)
        os.makedirs(cfg.dirs.outbox_dir, exist_ok=True)

    async def check_online(self, timeout: float = 1.5) -> bool:
        return True

    async def start_agy_process(self, agy_args: List[str]) -> asyncio.subprocess.Process:
        kwargs: Dict[str, Any] = {
            "stdout": asyncio.subprocess.PIPE,
            "stderr": asyncio.subprocess.PIPE,
        }
        if sys.platform == "win32":
            kwargs["creationflags"] = _win_creation_flags()
        return await asyncio.create_subprocess_exec(*agy_args, **kwargs)

    async def start_ollama_chat(self, payload_json: str) -> asyncio.subprocess.Process:
        # Use Python inline streaming via urllib or curl for cross-platform reliability
        py_script = (
            "import sys, urllib.request; "
            f"req = urllib.request.Request({self.cfg.ollama_url + '/api/chat'!r}, data=sys.stdin.buffer.read(), headers={{'Content-Type':'application/json'}}); "
            "resp = urllib.request.urlopen(req, timeout=120); "
            "[sys.stdout.buffer.write(line) or sys.stdout.buffer.flush() for line in resp]"
        )
        kwargs: Dict[str, Any] = {
            "stdin": asyncio.subprocess.PIPE,
            "stdout": asyncio.subprocess.PIPE,
            "stderr": asyncio.subprocess.PIPE,
        }
        if sys.platform == "win32":
            kwargs["creationflags"] = _win_creation_flags()
        proc = await asyncio.create_subprocess_exec(sys.executable, "-c", py_script, **kwargs)
        if proc.stdin:
            proc.stdin.write(payload_json.encode("utf-8"))
            await proc.stdin.drain()
            proc.stdin.close()
        return proc

    async def list_ollama_models(self) -> List[Dict[str, str]]:
        def _fetch() -> List[Dict[str, str]]:
            try:
                with urllib.request.urlopen(f"{self.cfg.ollama_url}/api/tags", timeout=2.0) as resp:
                    data = json.loads(resp.read().decode("utf-8"))
                    return [
                        {"id": f"ollama:{m['name']}", "name": f"Local: {m['name']}", "type": "local", "badge": "Ollama / Local"}
                        for m in data.get("models", [])
                        if m.get("name") and not m["name"].startswith("nomic")
                    ]
            except Exception:
                return []
        return await asyncio.to_thread(_fetch)

    def _brain_dir(self) -> str:
        return os.path.join(os.path.expanduser("~"), ".gemini", "antigravity-cli", "brain")

    async def list_agy_conversations(self, limit: int = 100) -> List[Dict[str, Any]]:
        def _scan() -> List[Dict[str, Any]]:
            brain = self._brain_dir()
            if not os.path.isdir(brain):
                return []
            items: List[Dict[str, Any]] = []
            for conv_path in glob.glob(os.path.join(brain, "*")):
                conv_id = os.path.basename(conv_path)
                transcript = os.path.join(conv_path, ".system_generated", "logs", "transcript.jsonl")
                if not os.path.isfile(transcript):
                    continue
                mtime = datetime.fromtimestamp(os.path.getmtime(transcript)).isoformat()
                title = conv_id[:8]
                msg_count = 0
                try:
                    with open(transcript, "r", encoding="utf-8", errors="replace") as f:
                        for line in f:
                            msg_count += 1
                            if title == conv_id[:8]:
                                obj = json.loads(line)
                                if obj.get("type") == "USER_INPUT" and obj.get("content"):
                                    title = str(obj["content"]).strip().splitlines()[0][:55]
                except Exception:
                    pass
                items.append({"id": conv_id, "title": title, "updated_at": mtime, "message_count": msg_count})
            items.sort(key=lambda x: x["updated_at"], reverse=True)
            return items[:limit]
        return await asyncio.to_thread(_scan)

    async def get_agy_conversation(self, conv_id: str) -> Optional[Dict[str, Any]]:
        def _read() -> Optional[Dict[str, Any]]:
            transcript = os.path.join(self._brain_dir(), conv_id, ".system_generated", "logs", "transcript.jsonl")
            if not os.path.isfile(transcript):
                return None
            msgs: List[Dict[str, str]] = []
            try:
                with open(transcript, "r", encoding="utf-8", errors="replace") as f:
                    for line in f:
                        obj = json.loads(line)
                        t = obj.get("type")
                        content = (obj.get("content") or "").strip()
                        if t == "USER_INPUT" and content:
                            msgs.append({"role": "user", "content": content})
                        elif t == "PLANNER_RESPONSE" and content:
                            msgs.append({"role": "agent", "content": content})
            except Exception:
                return None
            return {"id": conv_id, "messages": msgs}
        return await asyncio.to_thread(_read)

    async def save_to_inbox(self, local_tmp_path: str, clean_name: str) -> str:
        dest = os.path.join(self.cfg.dirs.inbox_dir, clean_name)
        def _copy() -> None:
            with open(local_tmp_path, "rb") as src, open(dest, "wb") as dst:
                dst.write(src.read())
        await asyncio.to_thread(_copy)
        return dest

    async def list_outbox(self) -> List[Dict[str, Any]]:
        def _ls() -> List[Dict[str, Any]]:
            p = self.cfg.dirs.outbox_dir
            os.makedirs(p, exist_ok=True)
            return [
                {"name": f, "size": os.path.getsize(os.path.join(p, f)), "mtime": os.path.getmtime(os.path.join(p, f))}
                for f in sorted(os.listdir(p))
                if os.path.isfile(os.path.join(p, f))
            ]
        return await asyncio.to_thread(_ls)

    async def stream_outbox_file(self, filename: str) -> AsyncIterator[bytes]:
        path = os.path.join(self.cfg.dirs.outbox_dir, filename)
        with open(path, "rb") as f:
            while True:
                chunk = f.read(65536)
                if not chunk:
                    break
                yield chunk

    async def delete_outbox_file(self, filename: str) -> None:
        path = os.path.join(self.cfg.dirs.outbox_dir, filename)
        if os.path.isfile(path):
            os.remove(path)

    async def get_telemetry(self) -> Dict[str, Any]:
        def _probe() -> Dict[str, Any]:
            import subprocess
            res: Dict[str, Any] = {"gpu": None, "cpu_temp": None, "ram": None}
            try:
                out = subprocess.check_output(
                    ["nvidia-smi", "--query-gpu=name,memory.used,memory.total,temperature.gpu", "--format=csv,noheader,nounits"],
                    timeout=2,
                ).decode().strip().split(", ")
                res["gpu"] = {"name": out[0], "vram_used_mb": round(float(out[1])), "vram_total_mb": round(float(out[2])), "temp_c": round(float(out[3]))}
            except Exception:
                pass
            try:
                if os.path.isfile("/proc/meminfo"):
                    m = {k: int(v.split()[0]) for k, v in (l.split(":") for l in open("/proc/meminfo") if ":" in l)}
                    tot = m.get("MemTotal", 0) // 1024
                    avail = m.get("MemAvailable", 0) // 1024
                    res["ram"] = {"used_mb": tot - avail, "total_mb": tot}
            except Exception:
                pass
            try:
                temps = [int(open(p).read().strip()) / 1000 for p in glob.glob("/sys/class/hwmon/hwmon*/temp1_input")[:4]]
                if temps:
                    res["cpu_temp"] = round(max(temps), 1)
            except Exception:
                pass
            return res
        return await asyncio.to_thread(_probe)


def create_runner(cfg: GatewaySettings) -> BaseRunner:
    return SSHRunner(cfg) if cfg.mode == "gateway" else LocalRunner(cfg)

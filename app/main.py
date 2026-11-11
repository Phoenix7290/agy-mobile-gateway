import os
import json
import shlex
import socket
import asyncio
from datetime import datetime
from typing import Optional, List, Dict, Any
from fastapi import FastAPI, HTTPException
from fastapi.responses import HTMLResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel
import wakeonlan

app = FastAPI(title="AGY Mobile Gateway", version="2.1.0")

TARGET_HOST = os.getenv("TARGET_HOST", "192.168.1.190")
TARGET_USER = os.getenv("TARGET_USER", "ai")
WOL_MAC = os.getenv("WOL_MAC", "bc:fc:e7:ca:84:05")
SSH_KEY = os.getenv("SSH_KEY", "/root/.ssh/id_ed25519")
STATIC_DIR = os.path.join(os.path.dirname(__file__), "static")
DATA_DIR = os.getenv("DATA_DIR", "/data")
HISTORY_FILE = os.path.join(DATA_DIR, "history.json")

if os.path.exists(STATIC_DIR):
    app.mount("/static", StaticFiles(directory=STATIC_DIR), name="static")

DEFAULT_AGY_MODELS = [
    {"id": "gemini-3.8-flash-high", "name": "Gemini 3.8 Flash (High)", "type": "cloud", "badge": "Rápido"},
    {"id": "gemini-4-argon-high", "name": "Gemini 4 Argon (High)", "type": "cloud", "badge": "Raciocínio"},
    {"id": "claude-sonnet-5-5-high", "name": "Claude Sonnet 5.5 (High)", "type": "cloud", "badge": "Engenharia"},
    {"id": "claude-opus-5-5-high", "name": "Claude Opus 5.5 (High)", "type": "cloud", "badge": "Top"},
    {"id": "gpt-oss-120b-medium", "name": "GPT-OSS 120B (Medium)", "type": "cloud", "badge": "Open"}
]

class ChatRequest(BaseModel):
    prompt: str
    conversation_id: Optional[str] = None
    model: Optional[str] = "gemini-3.8-flash-high"

class WolRequest(BaseModel):
    mac: Optional[str] = None

def check_target_online(host: str, port: int = 22, timeout: float = 1.5) -> bool:
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except (socket.timeout, ConnectionRefusedError, OSError):
        return False

def load_history_data() -> Dict[str, Any]:
    if not os.path.exists(HISTORY_FILE):
        return {"conversations": {}}
    try:
        with open(HISTORY_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return {"conversations": {}}

def save_history_data(data: Dict[str, Any]):
    os.makedirs(os.path.dirname(HISTORY_FILE), exist_ok=True)
    with open(HISTORY_FILE, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)

def append_to_history(conv_id: str, prompt: str, response: str, model: str):
    data = load_history_data()
    convs = data.setdefault("conversations", {})
    now = datetime.now().isoformat()
    
    if conv_id not in convs:
        title = prompt[:48].replace("\n", " ").strip()
        if len(prompt) > 48:
            title += "..."
        convs[conv_id] = {
            "id": conv_id,
            "title": title or "Nova Conversa",
            "model": model,
            "created_at": now,
            "updated_at": now,
            "messages": []
        }
    
    conv = convs[conv_id]
    conv["updated_at"] = now
    conv["model"] = model
    conv["messages"].append({"role": "user", "content": prompt, "timestamp": now})
    if response:
        conv["messages"].append({"role": "agent", "content": response, "timestamp": now})
    
    save_history_data(data)

@app.get("/", response_class=HTMLResponse)
async def serve_index():
    index_path = os.path.join(STATIC_DIR, "index.html")
    if os.path.exists(index_path):
        with open(index_path, "r", encoding="utf-8") as f:
            return HTMLResponse(content=f.read())
    return HTMLResponse("<h3>AGY Mobile Gateway UI not found.</h3>", status_code=404)

@app.get("/api/status")
async def get_status():
    is_online = await asyncio.to_thread(check_target_online, TARGET_HOST, 22, 1.5)
    return {
        "online": is_online,
        "target_host": TARGET_HOST,
        "wol_mac": WOL_MAC,
        "target_user": TARGET_USER,
    }

@app.post("/api/wol")
async def trigger_wol(req: Optional[WolRequest] = None):
    mac_to_wake = (req and req.mac) or WOL_MAC
    try:
        wakeonlan.send_magic_packet(mac_to_wake)
        return {"ok": True, "message": f"Magic Packet enviado para {mac_to_wake}"}
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Erro ao enviar WoL: {str(e)}")

@app.get("/api/models")
async def get_models():
    models = list(DEFAULT_AGY_MODELS)
    is_online = await asyncio.to_thread(check_target_online, TARGET_HOST, 22, 1.0)
    if is_online:
        try:
            cmd = [
                "ssh", "-i", SSH_KEY, "-o", "StrictHostKeyChecking=accept-new",
                "-o", "BatchMode=yes", "-o", "ConnectTimeout=2",
                f"{TARGET_USER}@{TARGET_HOST}",
                "curl -s --max-time 2 http://127.0.0.1:11434/api/tags"
            ]
            proc = await asyncio.create_subprocess_exec(*cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
            stdout, _ = await asyncio.wait_for(proc.communicate(), timeout=3.0)
            if proc.returncode == 0 and stdout:
                tags_data = json.loads(stdout.decode())
                for m in tags_data.get("models", []):
                    m_name = m.get("name")
                    if m_name and not m_name.startswith("nomic"):
                        models.append({
                            "id": f"ollama:{m_name}",
                            "name": f"Local: {m_name}",
                            "type": "local",
                            "badge": "Ollama / GPU"
                        })
        except Exception:
            pass
    return {"models": models}

@app.get("/api/conversations")
async def list_conversations():
    data = load_history_data()
    convs = list(data.get("conversations", {}).values())
    convs.sort(key=lambda c: c.get("updated_at", ""), reverse=True)
    return {
        "conversations": [
            {
                "id": c["id"],
                "title": c.get("title", "Conversa"),
                "model": c.get("model", "default"),
                "created_at": c.get("created_at"),
                "updated_at": c.get("updated_at"),
                "message_count": len(c.get("messages", []))
            }
            for c in convs
        ]
    }

@app.get("/api/conversations/{conv_id}")
async def get_conversation(conv_id: str):
    data = load_history_data()
    conv = data.get("conversations", {}).get(conv_id)
    if not conv:
        raise HTTPException(status_code=404, detail="Conversa não encontrada")
    return conv

@app.delete("/api/conversations/{conv_id}")
async def delete_conversation(conv_id: str):
    data = load_history_data()
    convs = data.get("conversations", {})
    if conv_id in convs:
        del convs[conv_id]
        save_history_data(data)
        return {"ok": True}
    raise HTTPException(status_code=404, detail="Conversa não encontrada")

@app.post("/api/chat")
async def chat_stream(req: ChatRequest):
    if not req.prompt.strip():
        raise HTTPException(status_code=400, detail="Prompt não pode ser vazio")

    selected_model = req.model or "gemini-3.8-flash-high"

    async def event_generator():
        is_online = await asyncio.to_thread(check_target_online, TARGET_HOST, 22, 1.5)
        if not is_online:
            payload = {
                "type": "error",
                "error": f"CachyCore ({TARGET_HOST}) está inacessível. O computador pode estar desligado. Use o botão WoL para ligá-lo."
            }
            yield f"data: {json.dumps(payload)}\n\n"
            yield "data: [DONE]\n\n"
            return

        current_conv_id = req.conversation_id or f"conv-{int(datetime.now().timestamp())}"
        accumulated_text = ""
        emitted_any = False

        # Mode A: Local Ollama Model
        if selected_model.startswith("ollama:"):
            ollama_model = selected_model.replace("ollama:", "")
            yield f"data: {json.dumps({'type': 'init', 'conversation_id': current_conv_id, 'model': selected_model})}\n\n"
            
            payload_json = json.dumps({
                "model": ollama_model,
                "prompt": req.prompt,
                "stream": True
            })
            
            cmd = [
                "ssh", "-i", SSH_KEY, "-o", "StrictHostKeyChecking=accept-new",
                "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
                f"{TARGET_USER}@{TARGET_HOST}",
                f"curl -s -N -X POST http://127.0.0.1:11434/api/generate -d {shlex.quote(payload_json)}"
            ]
            
            try:
                proc = await asyncio.create_subprocess_exec(*cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE)
                while True:
                    try:
                        line = await asyncio.wait_for(proc.stdout.readline(), timeout=8.0)
                    except asyncio.TimeoutError:
                        if proc.returncode is not None:
                            break
                        yield ": ping\n\n"
                        continue

                    if not line:
                        break
                    line_str = line.decode("utf-8", errors="replace").strip()
                    if not line_str:
                        continue
                    try:
                        chunk = json.loads(line_str)
                        token = chunk.get("response", "")
                        if token:
                            emitted_any = True
                            accumulated_text += token
                            yield f"data: {json.dumps({'type': 'delta', 'text': token})}\n\n"
                        if chunk.get("done"):
                            break
                    except json.JSONDecodeError:
                        continue
                await proc.wait()
                yield f"data: {json.dumps({'type': 'done', 'conversation_id': current_conv_id, 'status': 'SUCCESS'})}\n\n"
            except Exception as e:
                yield f"data: {json.dumps({'type': 'error', 'error': str(e)})}\n\n"

        # Mode B: Antigravity AGY Cloud Model
        else:
            agy_cmd = f"agy -p {shlex.quote(req.prompt)} --output-format stream-json --dangerously-skip-permissions"
            if req.conversation_id:
                agy_cmd += f" --conversation {shlex.quote(req.conversation_id)}"
            if selected_model:
                agy_cmd += f" --model {shlex.quote(selected_model)}"

            ssh_cmd = [
                "ssh", "-i", SSH_KEY, "-o", "StrictHostKeyChecking=accept-new",
                "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
                f"{TARGET_USER}@{TARGET_HOST}",
                agy_cmd
            ]

            try:
                proc = await asyncio.create_subprocess_exec(
                    *ssh_cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
                )
            except Exception as e:
                payload = {"type": "error", "error": f"Falha ao disparar processo SSH: {str(e)}"}
                yield f"data: {json.dumps(payload)}\n\n"
                yield "data: [DONE]\n\n"
                return

            try:
                while True:
                    # 8-second heartbeat timeout prevents mobile browsers from dropping the SSE connection
                    try:
                        line = await asyncio.wait_for(proc.stdout.readline(), timeout=8.0)
                    except asyncio.TimeoutError:
                        if proc.returncode is not None:
                            break
                        yield ": ping\n\n"
                        continue

                    if not line:
                        break
                    line_str = line.decode("utf-8", errors="replace").strip()
                    if not line_str:
                        continue

                    try:
                        data = json.loads(line_str)
                    except json.JSONDecodeError:
                        continue

                    event_type = data.get("event")
                    if event_type == "init":
                        current_conv_id = data.get("conversation_id", current_conv_id)
                        yield f"data: {json.dumps({'type': 'init', 'conversation_id': current_conv_id, 'model': selected_model})}\n\n"
                    elif event_type == "step_update":
                        step = data.get("step_update", {})
                        delta = step.get("text_delta")
                        if delta:
                            emitted_any = True
                            accumulated_text += delta
                            yield f"data: {json.dumps({'type': 'delta', 'text': delta})}\n\n"
                        tool_calls = step.get("tool_calls")
                        if tool_calls and isinstance(tool_calls, list):
                            tools = [t.get("name") for t in tool_calls if isinstance(t, dict)]
                            yield f"data: {json.dumps({'type': 'tool', 'tools': tools})}\n\n"
                    elif event_type == "result":
                        res = data.get("result", {})
                        conv = res.get("conversation_id", current_conv_id)
                        yield f"data: {json.dumps({'type': 'done', 'conversation_id': conv, 'status': res.get('status')})}\n\n"

                await proc.wait()
                if proc.returncode != 0 and not emitted_any:
                    err_bytes = await proc.stderr.read()
                    err_msg = err_bytes.decode("utf-8", errors="replace").strip()
                    yield f"data: {json.dumps({'type': 'error', 'error': err_msg or f'Processo saiu com código {proc.returncode}'})}\n\n"

            except asyncio.CancelledError:
                # Do not immediately terminate if task is still running: read remaining in background
                pass
            except Exception as e:
                yield f"data: {json.dumps({'type': 'error', 'error': str(e)})}\n\n"

        # Persist conversation turn into history
        if emitted_any and accumulated_text:
            try:
                append_to_history(current_conv_id, req.prompt, accumulated_text, selected_model)
            except Exception:
                pass

        yield "data: [DONE]\n\n"

    return StreamingResponse(
        event_generator(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no"
        }
    )

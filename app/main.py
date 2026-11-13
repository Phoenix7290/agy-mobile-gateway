import os
import re
import json
import uuid
import shlex
import socket
import asyncio
import mimetypes
from typing import Optional, List, Dict, Any
from fastapi import FastAPI, HTTPException, Header, Query, UploadFile, File
from fastapi.responses import HTMLResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel
import wakeonlan

from db import GatewayDB

app = FastAPI(title="AGY Mobile Gateway", version="3.1.0")

TARGET_HOST = os.getenv("TARGET_HOST", "192.168.1.190")
TARGET_USER = os.getenv("TARGET_USER", "ai")
WOL_MAC = os.getenv("WOL_MAC", "bc:fc:e7:ca:84:05")
SSH_KEY = os.getenv("SSH_KEY", "/root/.ssh/id_ed25519")
STATIC_DIR = os.path.join(os.path.dirname(__file__), "static")
DATA_DIR = os.getenv("DATA_DIR", "/data")
DB_PATH = os.path.join(DATA_DIR, "gateway.db")

db = GatewayDB(DB_PATH)
JOB_NOTIFIERS: Dict[str, asyncio.Condition] = {}
RUNNING_TASKS: Dict[str, asyncio.Task] = {}

if os.path.exists(STATIC_DIR):
    app.mount("/static", StaticFiles(directory=STATIC_DIR), name="static")

DEFAULT_AGY_MODELS = [
    {"id": "gemini-3.8-flash-high", "name": "Gemini 3.8 Flash (High)", "type": "cloud", "badge": "Rápido"},
    {"id": "gemini-4-argon-high", "name": "Gemini 4 Argon (High)", "type": "cloud", "badge": "Raciocínio"},
    {"id": "claude-sonnet-5-5-high", "name": "Claude Sonnet 5.5 (High)", "type": "cloud", "badge": "Engenharia"},
    {"id": "claude-opus-5-5-high", "name": "Claude Opus 5.5 (High)", "type": "cloud", "badge": "Top"},
    {"id": "gpt-oss-120b-medium", "name": "GPT-OSS 120B (Medium)", "type": "cloud", "badge": "Open"},
]

PERMISSION_MODES: Dict[str, List[str]] = {
    "readonly": ["--mode", "plan"],
    "copilot": ["--mode", "accept-edits"],
    "autonomous": ["--dangerously-skip-permissions"],
}


class JobCreateRequest(BaseModel):
    prompt: str
    conversation_id: Optional[str] = None
    model: Optional[str] = "gemini-3.8-flash-high"
    permission_mode: Optional[str] = "copilot"
    attached_files: Optional[List[str]] = None


class WolRequest(BaseModel):
    mac: Optional[str] = None


def check_target_online(host: str, port: int = 22, timeout: float = 1.5) -> bool:
    try:
        with socket.create_connection((host, port), timeout=timeout):
            return True
    except (socket.timeout, ConnectionRefusedError, OSError):
        return False


async def run_ssh_command(cmd_str: str, timeout: float = 5.0) -> Optional[str]:
    ssh_cmd = [
        "ssh", "-i", SSH_KEY,
        "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
        "-o", "BatchMode=yes", "-o", f"ConnectTimeout={int(timeout)}",
        f"{TARGET_USER}@{TARGET_HOST}",
        cmd_str,
    ]
    try:
        proc = await asyncio.create_subprocess_exec(
            *ssh_cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
        )
        stdout, _ = await asyncio.wait_for(proc.communicate(), timeout=timeout)
        if proc.returncode == 0:
            return stdout.decode("utf-8", errors="replace")
    except Exception:
        pass
    return None


async def _emit_event(job_id: str, event_type: str, payload: Dict[str, Any]) -> None:
    db.append_job_event(job_id, event_type, payload)
    cond = JOB_NOTIFIERS.get(job_id)
    if cond:
        async with cond:
            cond.notify_all()


async def _run_ollama_job(job_id: str, conv_id: str, prompt: str, selected_model: str) -> None:
    ollama_model = selected_model.replace("ollama:", "", 1)
    title = prompt.strip().splitlines()[0][:60] or "Conversa Local"
    db.ensure_local_conversation(conv_id, title, selected_model)
    prior_messages = db.get_local_messages(conv_id)
    db.append_local_message(conv_id, "user", prompt)

    await _emit_event(job_id, "init", {"type": "init", "conversation_id": conv_id, "model": selected_model})

    payload_json = json.dumps({
        "model": ollama_model,
        "messages": prior_messages + [{"role": "user", "content": prompt}],
        "stream": True,
    })

    cmd = [
        "ssh", "-i", SSH_KEY,
        "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
        "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
        f"{TARGET_USER}@{TARGET_HOST}",
        f"curl -s -N -X POST http://127.0.0.1:11434/api/chat -d {shlex.quote(payload_json)}",
    ]

    accumulated = ""
    try:
        proc = await asyncio.create_subprocess_exec(
            *cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
        )
        while True:
            line = await proc.stdout.readline()  # type: ignore
            if not line:
                break
            line_str = line.decode("utf-8", errors="replace").strip()
            if not line_str:
                continue
            try:
                chunk = json.loads(line_str)
            except json.JSONDecodeError:
                continue

            if chunk.get("error"):
                raise RuntimeError(chunk["error"])

            token = chunk.get("message", {}).get("content", "")
            if token:
                accumulated += token
                await _emit_event(job_id, "delta", {"type": "delta", "text": token})
            if chunk.get("done"):
                break

        await proc.wait()
        if proc.returncode != 0 and not accumulated:
            err_bytes = await proc.stderr.read()  # type: ignore
            raise RuntimeError(err_bytes.decode("utf-8", errors="replace").strip() or f"Erro Ollama ({proc.returncode})")

        if accumulated:
            db.append_local_message(conv_id, "assistant", accumulated)

        db.update_job(job_id, conversation_id=conv_id, status="completed")
        await _emit_event(job_id, "done", {"type": "done", "conversation_id": conv_id, "status": "SUCCESS", "executed_tools": []})
    except Exception as e:
        err_msg = str(e)
        db.update_job(job_id, status="error", error=err_msg)
        await _emit_event(job_id, "error", {"type": "error", "error": err_msg})
    finally:
        RUNNING_TASKS.pop(job_id, None)
        cond = JOB_NOTIFIERS.get(job_id)
        if cond:
            async with cond:
                cond.notify_all()


async def _run_agy_job(
    job_id: str,
    conversation_id: Optional[str],
    prompt: str,
    selected_model: str,
    permission_mode: str,
) -> None:
    perm_flags = " ".join(PERMISSION_MODES.get(permission_mode, PERMISSION_MODES["copilot"]))
    agy_cmd = f"agy -p {shlex.quote(prompt)} --output-format stream-json {perm_flags}"
    if conversation_id:
        agy_cmd += f" --conversation {shlex.quote(conversation_id)}"
    if selected_model:
        agy_cmd += f" --model {shlex.quote(selected_model)}"

    ssh_cmd = [
        "ssh", "-i", SSH_KEY,
        "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
        "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
        f"{TARGET_USER}@{TARGET_HOST}",
        agy_cmd,
    ]

    current_conv_id = conversation_id
    emitted_any = False
    executed_tools: List[str] = []

    try:
        proc = await asyncio.create_subprocess_exec(
            *ssh_cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
        )
        while True:
            line = await proc.stdout.readline()  # type: ignore
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
                db.update_job(job_id, conversation_id=current_conv_id)
                await _emit_event(
                    job_id,
                    "init",
                    {"type": "init", "conversation_id": current_conv_id, "model": selected_model},
                )

            elif event_type == "step_update":
                step = data.get("step_update", {})
                step_type = step.get("step_type")
                if step_type == "tool":
                    tool_name = step.get("tool_name", "tool")
                    tool_state = step.get("state", "ACTIVE")
                    params = step.get("tool_info", {}).get("parameters", {})
                    cmd_preview = params.get("CommandLine", "") or params.get("Path", "") or params.get("query", "")
                    if tool_state == "ACTIVE" and tool_name not in executed_tools:
                        executed_tools.append(tool_name)
                    await _emit_event(
                        job_id,
                        "tool",
                        {
                            "type": "tool",
                            "name": tool_name,
                            "state": tool_state,
                            "preview": str(cmd_preview)[:80],
                        },
                    )
                elif step_type == "agent_response":
                    delta = step.get("text_delta")
                    if delta:
                        emitted_any = True
                        await _emit_event(job_id, "delta", {"type": "delta", "text": delta})

            elif event_type == "result":
                res = data.get("result", {})
                current_conv_id = res.get("conversation_id", current_conv_id)
                final_resp = res.get("response", "").strip() if isinstance(res.get("response"), str) else ""
                if not emitted_any:
                    if final_resp:
                        emitted_any = True
                        await _emit_event(job_id, "delta", {"type": "delta", "text": final_resp})
                    elif executed_tools:
                        emitted_any = True
                        tools_bullet = "\n".join(f"- `{t}`" for t in executed_tools)
                        msg = f"*Instruções executadas no CachyCore com sucesso.*\n\n**Ações executadas:**\n{tools_bullet}"
                        await _emit_event(job_id, "delta", {"type": "delta", "text": msg})
                    else:
                        emitted_any = True
                        await _emit_event(job_id, "delta", {"type": "delta", "text": "*Instrução concluída com sucesso no CachyCore.*"})

                db.update_job(job_id, conversation_id=current_conv_id, status="completed")
                await _emit_event(
                    job_id,
                    "done",
                    {
                        "type": "done",
                        "conversation_id": current_conv_id,
                        "status": res.get("status", "SUCCESS"),
                        "executed_tools": executed_tools,
                        "response": final_resp,
                    },
                )

        await proc.wait()
        job = db.get_job(job_id)
        if proc.returncode != 0 and not emitted_any:
            err_bytes = await proc.stderr.read()  # type: ignore
            err_msg = err_bytes.decode("utf-8", errors="replace").strip() or f"Processo saiu com código {proc.returncode}"
            db.update_job(job_id, status="error", error=err_msg)
            await _emit_event(job_id, "error", {"type": "error", "error": err_msg})
        elif not emitted_any:
            if executed_tools:
                tools_bullet = "\n".join(f"- `{t}`" for t in executed_tools)
                msg = f"*Instruções executadas no CachyCore com sucesso.*\n\n**Ações executadas:**\n{tools_bullet}"
            else:
                msg = "*Instrução concluída com sucesso no CachyCore.*"
            await _emit_event(job_id, "delta", {"type": "delta", "text": msg})
            emitted_any = True
            db.update_job(job_id, conversation_id=current_conv_id, status="completed")
            await _emit_event(
                job_id,
                "done",
                {
                    "type": "done",
                    "conversation_id": current_conv_id,
                    "status": "SUCCESS",
                    "executed_tools": executed_tools,
                },
            )
        elif job and job["status"] == "running":
            db.update_job(job_id, conversation_id=current_conv_id, status="completed")
            await _emit_event(
                job_id,
                "done",
                {
                    "type": "done",
                    "conversation_id": current_conv_id,
                    "status": "SUCCESS",
                    "executed_tools": executed_tools,
                },
            )
    except Exception as e:
        err_msg = str(e)
        db.update_job(job_id, status="error", error=err_msg)
        await _emit_event(job_id, "error", {"type": "error", "error": err_msg})
    finally:
        RUNNING_TASKS.pop(job_id, None)
        cond = JOB_NOTIFIERS.get(job_id)
        if cond:
            async with cond:
                cond.notify_all()


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
        stdout = await run_ssh_command("curl -s --max-time 2 http://127.0.0.1:11434/api/tags", timeout=3.0)
        if stdout:
            try:
                tags_data = json.loads(stdout)
                for m in tags_data.get("models", []):
                    m_name = m.get("name")
                    if m_name and not m_name.startswith("nomic"):
                        models.append({
                            "id": f"ollama:{m_name}",
                            "name": f"Local: {m_name}",
                            "type": "local",
                            "badge": "Ollama / GPU",
                        })
            except Exception:
                pass
    return {"models": models}


@app.get("/api/conversations")
async def list_conversations():
    merged: List[Dict[str, Any]] = []

    is_online = await asyncio.to_thread(check_target_online, TARGET_HOST, 22, 1.5)
    if is_online:
        stdout = await run_ssh_command("python3 /home/ai/bin/agy-gateway-sync.py list 100", timeout=6.0)
        if stdout:
            try:
                data = json.loads(stdout)
                for c in data.get("conversations", []):
                    c["engine"] = "agy"
                    merged.append(c)
            except Exception:
                pass

    merged.extend(db.list_local_conversations())
    merged.sort(key=lambda c: c.get("updated_at") or "", reverse=True)
    return {"conversations": merged}


@app.get("/api/conversations/{conv_id}")
async def get_conversation(conv_id: str):
    local_conv = db.get_local_conversation(conv_id)
    if local_conv:
        active_jobs = db.get_active_jobs()
        matching = [j["id"] for j in active_jobs if j.get("conversation_id") == conv_id]
        local_conv["active_job_id"] = matching[0] if matching else None
        return local_conv

    is_online = await asyncio.to_thread(check_target_online, TARGET_HOST, 22, 1.5)
    if is_online:
        stdout = await run_ssh_command(f"python3 /home/ai/bin/agy-gateway-sync.py get {shlex.quote(conv_id)}", timeout=5.0)
        if stdout:
            try:
                data = json.loads(stdout)
                if "messages" in data:
                    data["engine"] = "agy"
                    active_jobs = db.get_active_jobs()
                    matching = [j["id"] for j in active_jobs if j.get("conversation_id") == conv_id]
                    data["active_job_id"] = matching[0] if matching else None
                    return data
            except Exception:
                pass

    raise HTTPException(status_code=404, detail="Conversa não encontrada")


@app.delete("/api/conversations/{conv_id}")
async def delete_conversation(conv_id: str):
    deleted = db.delete_local_conversation(conv_id)
    return {"ok": True, "deleted_local": deleted}


@app.post("/api/jobs")
async def create_job(req: JobCreateRequest):
    prompt = req.prompt.strip()
    if not prompt:
        raise HTTPException(status_code=400, detail="Prompt não pode ser vazio")

    is_online = await asyncio.to_thread(check_target_online, TARGET_HOST, 22, 1.5)
    if not is_online:
        raise HTTPException(
            status_code=503,
            detail=f"CachyCore ({TARGET_HOST}) está inacessível. Use o botão WoL para ligá-lo.",
        )

    if req.attached_files:
        files_note = (
            "[Arquivos anexados pelo usuário em /home/ai/agy-inbox/: "
            + ", ".join(req.attached_files)
            + ". Inspecione-os usando read_file conforme necessário.]\n\n"
        )
        prompt = files_note + prompt

    selected_model = req.model or "gemini-3.8-flash-high"
    permission_mode = req.permission_mode if req.permission_mode in PERMISSION_MODES else "copilot"
    engine = "ollama" if selected_model.startswith("ollama:") else "agy"

    conv_id = req.conversation_id
    if engine == "ollama" and (not conv_id or not conv_id.startswith("local-")):
        conv_id = f"local-{uuid.uuid4().hex[:10]}"

    job_id = uuid.uuid4().hex[:12]
    db.create_job(
        job_id=job_id,
        conversation_id=conv_id,
        engine=engine,
        model=selected_model,
        permission_mode=permission_mode,
        prompt=prompt,
    )

    JOB_NOTIFIERS[job_id] = asyncio.Condition()
    if engine == "ollama":
        task = asyncio.create_task(_run_ollama_job(job_id, conv_id, prompt, selected_model))  # type: ignore
    else:
        task = asyncio.create_task(_run_agy_job(job_id, conv_id, prompt, selected_model, permission_mode))
    RUNNING_TASKS[job_id] = task

    return {"job_id": job_id, "conversation_id": conv_id, "engine": engine, "status": "running"}


@app.get("/api/jobs/active")
async def get_active_jobs():
    return {"jobs": db.get_active_jobs()}


@app.get("/api/jobs/{job_id}")
async def get_job_status(job_id: str):
    job = db.get_job(job_id)
    if not job:
        raise HTTPException(status_code=404, detail="Job não encontrado")
    return job


@app.get("/api/jobs/{job_id}/events")
async def stream_job_events(
    job_id: str,
    after: int = Query(0, ge=0),
    last_event_id: Optional[str] = Header(None, alias="Last-Event-ID"),
):
    job = db.get_job(job_id)
    if not job:
        raise HTTPException(status_code=404, detail="Job não encontrado")

    cursor = after
    if last_event_id and last_event_id.isdigit():
        cursor = max(cursor, int(last_event_id))

    async def event_generator():
        nonlocal cursor
        cond = JOB_NOTIFIERS.get(job_id)

        while True:
            events = db.get_job_events_after(job_id, cursor)
            for ev in events:
                cursor = ev["seq"]
                yield f"id: {cursor}\ndata: {json.dumps(ev['payload'], ensure_ascii=False)}\n\n"

            current_job = db.get_job(job_id)
            if not current_job or current_job["status"] != "running":
                tail_events = db.get_job_events_after(job_id, cursor)
                for ev in tail_events:
                    cursor = ev["seq"]
                    yield f"id: {cursor}\ndata: {json.dumps(ev['payload'], ensure_ascii=False)}\n\n"
                yield "data: [DONE]\n\n"
                break

            if not cond:
                await asyncio.sleep(0.5)
                cond = JOB_NOTIFIERS.get(job_id)
                continue

            try:
                async with cond:
                    await asyncio.wait_for(cond.wait(), timeout=8.0)
            except asyncio.TimeoutError:
                yield ": ping\n\n"

    return StreamingResponse(
        event_generator(),
        media_type="text/event-stream",
        headers={
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )


# --- Phase 2: Uploads & Outbox Files ---

@app.post("/api/upload")
async def upload_file(file: UploadFile = File(...)):
    if not file.filename:
        raise HTTPException(status_code=400, detail="Nome de arquivo vazio")

    clean_name = re.sub(r"[^a-zA-Z0-9_.-]", "_", os.path.basename(file.filename))
    upload_dir = os.path.join(DATA_DIR, "uploads")
    os.makedirs(upload_dir, exist_ok=True)
    local_path = os.path.join(upload_dir, clean_name)

    content = await file.read()
    with open(local_path, "wb") as f:
        f.write(content)

    scp_cmd = [
        "scp", "-q", "-i", SSH_KEY,
        "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
        local_path,
        f"{TARGET_USER}@{TARGET_HOST}:/home/{TARGET_USER}/agy-inbox/{clean_name}",
    ]
    try:
        proc = await asyncio.create_subprocess_exec(*scp_cmd)
        await asyncio.wait_for(proc.wait(), timeout=20.0)
        if proc.returncode != 0:
            raise RuntimeError(f"SCP saiu com código {proc.returncode}")
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"Erro ao transferir arquivo para CachyCore: {str(e)}")

    remote_path = f"/home/{TARGET_USER}/agy-inbox/{clean_name}"
    return {
        "ok": True,
        "filename": clean_name,
        "path": remote_path,
        "size": len(content),
    }


@app.get("/api/outbox")
async def list_outbox():
    is_online = await asyncio.to_thread(check_target_online, TARGET_HOST, 22, 1.5)
    if not is_online:
        return {"files": []}

    py_cmd = (
        "python3 -c \"import os, json; d='/home/ai/agy-outbox'; "
        "files = [{'name': f, 'size': os.path.getsize(os.path.join(d, f)), "
        "'mtime': os.path.getmtime(os.path.join(d, f))} "
        "for f in os.listdir(d) if not f.startswith('.') and os.path.isfile(os.path.join(d, f))]; "
        "print(json.dumps(files))\""
    )
    stdout = await run_ssh_command(py_cmd, timeout=5.0)
    if stdout:
        try:
            files = json.loads(stdout)
            files.sort(key=lambda x: x.get("mtime", 0), reverse=True)
            return {"files": files}
        except Exception:
            pass
    return {"files": []}


@app.get("/api/outbox/{filename}")
async def download_outbox_file(filename: str):
    clean_name = re.sub(r"[^a-zA-Z0-9_.-]", "_", os.path.basename(filename))
    is_online = await asyncio.to_thread(check_target_online, TARGET_HOST, 22, 1.5)
    if not is_online:
        raise HTTPException(status_code=503, detail="CachyCore offline")

    ssh_cmd = [
        "ssh", "-i", SSH_KEY,
        "-o", "UserKnownHostsFile=/dev/null", "-o", "StrictHostKeyChecking=no",
        "-o", "BatchMode=yes",
        f"{TARGET_USER}@{TARGET_HOST}",
        f"cat {shlex.quote(f'/home/{TARGET_USER}/agy-outbox/{clean_name}')}",
    ]
    proc = await asyncio.create_subprocess_exec(
        *ssh_cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.DEVNULL
    )

    async def file_stream():
        while True:
            chunk = await proc.stdout.read(64 * 1024)
            if not chunk:
                break
            yield chunk
        await proc.wait()

    mime_type, _ = mimetypes.guess_type(clean_name)
    headers = {
        "Content-Disposition": f'attachment; filename="{clean_name}"',
    }
    return StreamingResponse(
        file_stream(),
        media_type=mime_type or "application/octet-stream",
        headers=headers,
    )


@app.delete("/api/outbox/{filename}")
async def delete_outbox_file(filename: str):
    clean_name = re.sub(r"[^a-zA-Z0-9_.-]", "_", os.path.basename(filename))
    cmd = f"rm -f {shlex.quote(f'/home/{TARGET_USER}/agy-outbox/{clean_name}')}"
    await run_ssh_command(cmd, timeout=3.0)
    return {"ok": True}

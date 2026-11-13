# AGENTS.md — AGY Mobile Gateway

Project-level instructions for AI coding agents and automated contributors working in this repository.

## 1. Tech Stack & Architecture
- **Backend (`app/`):** Python 3.12 + FastAPI (`app/main.py`) + SQLite WAL/FTS5 (`app/db.py`). Runs inside Docker (`agy-gateway`) on a Raspberry Pi (`network_mode: host`, port `8088`), communicating with the compute node via SSH/SCP (`BatchMode=yes`).
- **Terminal (`agy-ttyd`):** `ttyd` running on port `7681` with `-b /terminal`. FastAPI exposes `@app.websocket("/terminal/ws")` as a same-origin WebSocket proxy so the frontend's native `xterm.js` works across both LAN and Cloudflare Tunnel.
- **Frontend (`frontend/`):** Svelte 5 (runes `$state`, `$derived`, `$props`, `$effect`) + Vite + TypeScript + Tailwind CSS v4 + `@xterm/xterm`. Compiled output goes to `app/static/dist/`.

## 2. Strict UI & Design Rules
- **Zero Unicode Emojis in UI:** Never place Unicode emojis in Svelte components, HTML, status labels, or error messages. Use `src/components/Icon.svelte` with `src/lib/icons.ts` (`24x24`, `stroke-width="1.85"`, `stroke="currentColor"`).
- **Mobile-First Viewport:** Keep `max-width: 100vw; overflow-x: hidden;` and test narrow mobile widths (~375px). Always preserve `window.visualViewport` sizing in `TerminalModal.svelte`.

## 3. Security & Privacy Boundaries
- **Never commit secrets:** Do not commit `.env`, `ssh/id_*` private keys, API tokens, or `data/*.db` SQLite files.
- **Least Privilege:** The gateway executes non-privileged commands as the service user (`ai`). Administrative (`sudo`) tasks are strictly Human-in-the-Loop via the interactive `agy-ttyd` terminal.
- **Do not bypass permission modes:** Always respect the user-selected `permission_mode` (`readonly`, `copilot`, `autonomous`).

## 4. Build & Verification Checklist
Before committing any change:
1. **Backend syntax check:** `python3 -m py_compile app/main.py app/db.py`
2. **Frontend build:** `cd frontend && npm run build` (verify `app/static/dist/` updates cleanly with zero TypeScript/Svelte errors).
3. **Emoji check:** Ensure no Unicode emojis were introduced in `frontend/src/`.
4. **Live smoke test:** Verify `GET /`, `/api/status`, `/api/models`, `/api/features`, and `/api/settings` on the running gateway.

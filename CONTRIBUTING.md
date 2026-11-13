# Contributing to AGY Mobile Gateway

Thank you for contributing! Here is a quick guide to collaborating smoothly on this repository.

## Workflow
1. **Create a feature branch** from `main`:
   ```bash
   git checkout -b feat/your-feature-name
   ```
2. **Keep commits clean and descriptive** using Conventional Commits:
   - `feat(ui): ...`
   - `fix(api): ...`
   - `docs: ...`
   - `refactor(db): ...`
3. **Rebuild the frontend** whenever you modify files in `frontend/src/`:
   ```bash
   cd frontend
   npm install
   npm run build
   ```
   Commit both your source edits in `frontend/src/` and the compiled bundle in `app/static/dist/` so the Raspberry Pi Docker container can serve updates immediately without Node.js installed.

## Code Standards
- **Backend (Python):** Type-hint new Pydantic models and endpoints; keep SQLite queries thread-safe inside `GatewayDB` (`app/db.py`).
- **Frontend (Svelte 5 + TS):** Use Svelte 5 runes (`$state`, `$derived`, `$props`, `$effect`). Add new SVG icons to `frontend/src/lib/icons.ts` instead of using Unicode emojis.
- **Security:** Never commit `.env`, SSH private keys (`ssh/id_*`), Cloudflare tokens, or SQLite `.db` files.

## Pull Request Checklist
- [ ] `python3 -m py_compile app/main.py app/db.py` passes
- [ ] `cd frontend && npm run build` succeeds without errors
- [ ] Tested on mobile viewport (~375px) with no horizontal overflow
- [ ] No secrets or local database files staged

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
   Commit both your source edits in `frontend/src/` and the compiled bundle in `backend-go/internal/web/static/dist/` so the Go binary (`//go:embed`) and Raspberry Pi Docker build bundle updates cleanly without requiring Node.js on the Pi.

## Code Standards
- **Backend (Go):** Pure-Go zero-CGO (`modernc.org/sqlite`, stdlib `net/http`, `os/exec` with platform build tags in `internal/runner/proc_*.go`). Keep `GatewayDB` (`backend-go/internal/db/db.go`) safe for concurrent access and preserve SSE/JSON API contracts.
- **Frontend (Svelte 5 + TS):** Use Svelte 5 runes (`$state`, `$derived`, `$props`, `$effect`). Add new SVG icons to `frontend/src/lib/icons.ts` instead of using Unicode emojis.
- **Security:** Never commit `.env`, SSH private keys (`ssh/id_*`), Cloudflare tokens, or SQLite `.db` files.

## Pull Request Checklist
- [ ] `cd backend-go && go test -v -race ./...` passes
- [ ] `cd frontend && npm run build` succeeds without errors
- [ ] Tested on mobile viewport (~375px) with no horizontal overflow
- [ ] No secrets or local database files staged

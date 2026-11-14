## Summary
<!-- Briefly describe what this PR changes and why -->

## Type of Change
- [ ] Bug fix
- [ ] New feature
- [ ] UI / Mobile UX improvement
- [ ] Documentation

## Checklist
- [ ] `cd backend-go && go test -v -race ./...` passes
- [ ] `cd frontend && npm run build` compiled `backend-go/internal/web/static/dist/` cleanly
- [ ] Zero Unicode emojis added to UI components (used `Icon.svelte`)
- [ ] Tested on mobile viewport without horizontal overflow
- [ ] No `.env`, SSH keys, or `.db` files included

# Regras de UI & Ícones (AGY Mobile Gateway)

1. **Zero Emojis na Interface e Respostas de UI:**
   - É proibido usar emojis Unicode (`🚀`, `🔒`, `✅`, `⚠️`, `🖥️`, `☁️`, etc.) em botões, títulos, badges, status, drawers ou mensagens de erro da interface web.
   - Use **ícones SVG vetoriais** com traço uniforme (`stroke="currentColor"`, `fill="none"`, `viewBox="0 0 24 24"`, `stroke-width="1.85"`, `stroke-linecap="round"`, `stroke-linejoin="round"`).

2. **Padrão Baseado no Hegemonia:**
   - Na migração para Svelte 5 (Fase 3), adotar exatamente o padrão de `/home/ryanslx/code/games/hegemonia/apps/web/src/components/Icon.svelte` + `src/lib/icons.ts` (componente `<Icon name="..." size={18} />` sem dependências pesadas).
   - Enquanto o frontend estiver em HTML único (Fases 1–2), utilizar SVGs inline seguindo exatamente o mesmo padrão visual (`24x24`, `stroke-width="1.85"`, `currentColor`).

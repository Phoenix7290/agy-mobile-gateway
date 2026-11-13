<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import Icon from './Icon.svelte';

  let {
    open = $bindable(false),
  }: {
    open: boolean;
  } = $props();

  let termContainer: HTMLDivElement | undefined = $state();
  let termInputVal = $state('');
  let connected = $state(false);
  let statusMsg = $state('Desconectado');
  let modalHeight = $state('100dvh');
  let modalTop = $state('0px');

  let term: Terminal | null = null;
  let fitAddon: FitAddon | null = null;
  let ws: WebSocket | null = null;
  const encoder = new TextEncoder();
  const decoder = new TextDecoder('utf-8');

  function sendTextToTty(text: string) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const payload = encoder.encode('0' + text);
    ws.send(payload);
  }

  function sendResize(cols: number, rows: number) {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const msg = '1' + JSON.stringify({ columns: cols, rows: rows });
    ws.send(encoder.encode(msg));
  }

  function connectTerminal() {
    disconnectTerminal();
    if (!termContainer) return;

    term = new Terminal({
      cursorBlink: true,
      fontSize: 13,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
      theme: {
        background: '#09090b',
        foreground: '#e4e4e7',
        cursor: '#38bdf8',
      },
      scrollback: 2000,
    });
    fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(termContainer);

    setTimeout(() => {
      fitAddon?.fit();
    }, 50);

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${proto}//${window.location.host}/terminal/ws`;
    statusMsg = 'Conectando...';

    ws = new WebSocket(wsUrl, ['tty']);
    ws.binaryType = 'arraybuffer';

    ws.onopen = () => {
      connected = true;
      statusMsg = 'Conectado (ryanslx@CachyCore)';
      // ttyd requires AuthToken JSON first (empty token when no auth)
      ws?.send(encoder.encode(JSON.stringify({ AuthToken: '', columns: term?.cols ?? 80, rows: term?.rows ?? 24 })));
      if (term) {
        fitAddon?.fit();
        sendResize(term.cols, term.rows);
      }
    };

    ws.onmessage = (ev) => {
      if (!(ev.data instanceof ArrayBuffer)) return;
      const bytes = new Uint8Array(ev.data);
      if (bytes.length === 0) return;
      const cmd = String.fromCharCode(bytes[0]);
      if (cmd === '0') {
        const text = decoder.decode(bytes.subarray(1));
        term?.write(text);
      }
    };

    ws.onclose = () => {
      connected = false;
      statusMsg = 'Sessão encerrada';
    };

    ws.onerror = () => {
      connected = false;
      statusMsg = 'Erro de conexão WebSocket';
    };

    term.onData((data) => {
      sendTextToTty(data);
    });

    term.onResize(({ cols, rows }) => {
      sendResize(cols, rows);
    });
  }

  function disconnectTerminal() {
    if (ws) {
      ws.close();
      ws = null;
    }
    if (term) {
      term.dispose();
      term = null;
    }
    fitAddon = null;
    connected = false;
  }

  function handleKey(key: 'ESC' | 'TAB' | 'CTRL_C' | 'UP' | 'DOWN') {
    const map: Record<string, string> = {
      ESC: '\x1b',
      TAB: '\t',
      CTRL_C: '\x03',
      UP: '\x1b[A',
      DOWN: '\x1b[B',
    };
    sendTextToTty(map[key]);
  }

  function insertText(txt: string) {
    termInputVal += txt;
  }

  async function pasteClipboard() {
    try {
      const text = await navigator.clipboard.readText();
      if (text) {
        termInputVal += text;
      }
    } catch {
      // Fallback: user can paste directly in the input field
    }
  }

  function submitInput() {
    const val = termInputVal;
    sendTextToTty(val + '\r');
    termInputVal = '';
  }

  function updateViewport() {
    if (!open) return;
    if (window.visualViewport) {
      modalHeight = `${window.visualViewport.height}px`;
      modalTop = `${window.visualViewport.offsetTop}px`;
    } else {
      modalHeight = '100dvh';
      modalTop = '0px';
    }
    setTimeout(() => {
      if (fitAddon && term) {
        fitAddon.fit();
        sendResize(term.cols, term.rows);
      }
    }, 60);
  }

  $effect(() => {
    if (open && termContainer && !term) {
      updateViewport();
      connectTerminal();
    } else if (!open && term) {
      disconnectTerminal();
    }
  });

  onMount(() => {
    window.visualViewport?.addEventListener('resize', updateViewport);
    window.visualViewport?.addEventListener('scroll', updateViewport);
  });

  onDestroy(() => {
    window.visualViewport?.removeEventListener('resize', updateViewport);
    window.visualViewport?.removeEventListener('scroll', updateViewport);
    disconnectTerminal();
  });
</script>

{#if open}
  <div
    style="height: {modalHeight}; top: {modalTop};"
    class="fixed inset-x-0 bg-zinc-950 z-50 flex flex-col select-none"
  >
    <!-- Header -->
    <div class="h-12 bg-zinc-900 border-b border-zinc-800 px-3 flex items-center justify-between shrink-0">
      <div class="flex items-center gap-2 min-w-0">
        <span class="w-2 h-2 rounded-full {connected ? 'bg-emerald-500' : 'bg-amber-500'}"></span>
        <span class="text-xs font-mono font-bold text-zinc-200 truncate">{statusMsg}</span>
      </div>
      <div class="flex items-center gap-2 shrink-0">
        <button
          type="button"
          onclick={connectTerminal}
          title="Reconectar"
          class="p-1.5 rounded-lg bg-zinc-800 text-zinc-400 hover:text-zinc-100"
        >
          <Icon name="refresh" size={16} />
        </button>
        <button
          type="button"
          onclick={() => (open = false)}
          class="px-2.5 py-1 rounded-lg bg-rose-600/20 text-rose-400 border border-rose-500/30 hover:bg-rose-600/30 text-xs font-semibold"
        >
          Fechar
        </button>
      </div>
    </div>

    <!-- Native xterm.js Canvas -->
    <div bind:this={termContainer} class="flex-1 min-h-0 w-full bg-zinc-950 p-1 overflow-hidden"></div>

    <!-- Mobile Quick Keys & Input Bar -->
    <div class="bg-zinc-900/95 border-t border-zinc-800 p-2 flex flex-col gap-1.5 shrink-0">
      <div class="flex items-center gap-1.5 overflow-x-auto pb-0.5">
        <button type="button" onclick={() => handleKey('ESC')} class="px-2.5 py-1 rounded bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-[11px] font-mono border border-zinc-700 shrink-0">Esc</button>
        <button type="button" onclick={() => handleKey('TAB')} class="px-2.5 py-1 rounded bg-zinc-800 hover:bg-zinc-700 text-zinc-200 text-[11px] font-mono border border-zinc-700 shrink-0">Tab</button>
        <button type="button" onclick={() => handleKey('CTRL_C')} class="px-2.5 py-1 rounded bg-rose-950/60 hover:bg-rose-900/60 text-rose-300 text-[11px] font-mono border border-rose-800/60 shrink-0">Ctrl+C</button>
        <button type="button" onclick={() => handleKey('UP')} class="px-2 py-1 rounded bg-zinc-800 hover:bg-zinc-700 text-zinc-200 border border-zinc-700 shrink-0" aria-label="Cima">
          <Icon name="chevron-up" size={14} />
        </button>
        <button type="button" onclick={() => handleKey('DOWN')} class="px-2 py-1 rounded bg-zinc-800 hover:bg-zinc-700 text-zinc-200 border border-zinc-700 shrink-0" aria-label="Baixo">
          <Icon name="chevron-down" size={14} />
        </button>
        <button type="button" onclick={() => insertText('sudo ')} class="px-2.5 py-1 rounded bg-emerald-950/60 hover:bg-emerald-900/60 text-emerald-300 text-[11px] font-mono border border-emerald-800/60 shrink-0">sudo</button>
        <button type="button" onclick={pasteClipboard} class="px-2.5 py-1 rounded bg-sky-600/20 hover:bg-sky-600/30 text-sky-300 text-[11px] font-mono border border-sky-500/40 shrink-0 flex items-center gap-1">
          <Icon name="clipboard" size={12} />
          <span>Colar</span>
        </button>
      </div>
      <div class="flex items-center gap-1.5">
        <input
          type="text"
          bind:value={termInputVal}
          placeholder="Comando rápido / colar texto..."
          class="flex-1 bg-zinc-950 border border-zinc-700/80 rounded-lg px-2.5 py-1.5 text-xs font-mono text-zinc-100 placeholder-zinc-500 focus:outline-none focus:border-sky-500"
          onkeydown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              submitInput();
            }
          }}
        />
        <button
          type="button"
          onclick={submitInput}
          class="px-3 py-1.5 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-mono font-semibold shrink-0"
        >
          Enviar
        </button>
      </div>
    </div>
  </div>
{/if}

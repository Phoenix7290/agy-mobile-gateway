<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { marked } from 'marked';
  import Icon from './components/Icon.svelte';
  import Sidebar from './components/Sidebar.svelte';
  import TerminalModal from './components/TerminalModal.svelte';
  import OutboxModal from './components/OutboxModal.svelte';
  import {
    cancelJob,
    createJob,
    fetchConversation,
    fetchFeatures,
    fetchModels,
    fetchOutbox,
    fetchSettings,
    fetchStatus,
    fetchWatchdog,
    triggerWol,
    uploadFile,
    type ChatMessage,
    type GatewaySettings,
    type ModelInfo,
    type OutboxFile,
    type PermissionMode,
  } from './lib/api';

  // State
  let targetOnline = $state<boolean | null>(null);
  let gatewayMode = $state<'gateway' | 'standalone'>('gateway');
  let features = $state<Record<string, any>>({
    terminal: true,
    wol: true,
    watchdog: true,
    ollama: true,
    inbox_outbox: true,
  });
  let models = $state<ModelInfo[]>([
    { id: 'gemini-4-hydrogen-high', name: 'Gemini 4 Hydrogen (High)', type: 'cloud', badge: 'Hydrogen' },
    { id: 'gemini-4-hydrogen-xhigh', name: 'Gemini 4 Hydrogen (Extra)', type: 'cloud', badge: 'Extra' },
    { id: 'gemini-4-hydrogen-medium', name: 'Gemini 4 Hydrogen (Medium)', type: 'cloud', badge: 'Hydrogen' },
  ]);
  let settings = $state<GatewaySettings>({
    default_permission_mode: 'copilot',
    default_model: 'gemini-4-hydrogen-high',
    default_effort: '',
  });
  let selectedModel = $state('gemini-4-hydrogen-high');
  let permissionMode = $state<PermissionMode>('copilot');

  let conversationId = $state<string | null>(localStorage.getItem('agy_conv_id'));
  let messages = $state<ChatMessage[]>([]);
  let userInput = $state('');
  let attachedFiles = $state<{ name: string; path: string }[]>([]);
  let uploading = $state(false);

  // Streaming / Job state
  let isGenerating = $state(false);
  let activeJobId = $state<string | null>(localStorage.getItem('agy_active_job'));
  let activeJobLastSeq = $state<number>(Number(localStorage.getItem('agy_active_seq') || '0'));
  let liveStatus = $state('');
  let liveElapsed = $state(0);
  let streamAbort: AbortController | null = null;
  let elapsedTimer: ReturnType<typeof setInterval> | null = null;

  // Modals & Drawers
  let sidebarOpen = $state(false);
  let terminalOpen = $state(false);
  let outboxOpen = $state(false);
  let outboxFiles = $state<OutboxFile[]>([]);

  // Voice Dictation & Slash Commands
  let isListening = $state(false);
  let recognition = $state<any>(null);
  let slashMenuOpen = $state(false);

  const slashCommands = [
    { cmd: '/usage', desc: 'Cota restante de tokens/modelos' },
    { cmd: '/compact', desc: 'Compactar histórico da sessão' },
    { cmd: '/diff', desc: 'Exibir git status e diff atual' },
    { cmd: '/mem', desc: 'Consultar memória persistente (ai-memory)' },
    { cmd: '/plan', desc: 'Ativar modo Planejamento (Leitura)' },
    { cmd: '/clear', desc: 'Limpar e iniciar nova conversa' },
    { cmd: '/status', desc: 'Status do Watchdog & recursos' },
  ];

  let approvalNeeded = $state<{ tool: string; command: string } | null>(null);

  async function runSlashCommand(cmd: string) {
    slashMenuOpen = false;
    userInput = '';
    if (cmd === '/clear') {
      newConversation();
      return;
    }
    if (cmd === '/plan') {
      permissionMode = 'readonly';
      messages = [...messages, { role: 'agent', content: '**Modo Planejamento (`readonly`) ativado.** Nenhuma alteração será aplicada sem revisão.' }];
      scrollToBottom();
      return;
    }
    if (cmd === '/diff') {
      userInput = 'Execute git status -s e git diff no workspace atual e exiba a saída dentro de um bloco ```diff.';
      await handleSubmit();
      return;
    }
    if (cmd === '/mem') {
      userInput = 'Consulte no ai-memory: ';
      textareaEl?.focus();
      return;
    }
    if (cmd === '/status') {
      const [wd, feat] = await Promise.all([fetchWatchdog(), fetchFeatures()]);
      const summary = [
        `### Status do Gateway (v${feat.version})`,
        `- **CachyCore:** ${wd.online ? 'Online' : 'Offline'}`,
        `- **Verificações Watchdog:** ${wd.uptime_checks} (Quedas: ${wd.downtime_events})`,
        `- **Recursos:** ${Object.entries(feat.features).filter(([, v]) => v === true).map(([k]) => `\`${k}\``).join(', ')}`,
      ].join('\n');
      messages = [...messages, { role: 'agent', content: summary }];
      scrollToBottom();
      return;
    }
    if (cmd === '/compact' && !conversationId) {
      messages = [...messages, { role: 'agent', content: '> Não há conversa ativa para compactar. Envie uma mensagem ou carregue uma sessão primeiro.' }];
      scrollToBottom();
      return;
    }
    userInput = cmd;
    await handleSubmit();
  }

  // DOM Refs
  let chatContainer: HTMLElement | undefined = $state();
  let fileInputEl: HTMLInputElement | undefined = $state();
  let textareaEl: HTMLTextAreaElement | undefined = $state();

  async function scrollToBottom() {
    await tick();
    if (chatContainer) {
      chatContainer.scrollTop = chatContainer.scrollHeight;
    }
  }

  function escapeHtml(str: string): string {
    return str
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;');
  }

  const mdRenderer = new marked.Renderer();
  mdRenderer.code = ({ text, lang }: { text: string; lang?: string }) => {
    const isDiff = lang === 'diff' || (/^(---|\+\+\+|@@ )/m.test(text) && /^[+-]/m.test(text));
    if (isDiff) {
      const linesHtml = text
        .split('\n')
        .map((line) => {
          const esc = escapeHtml(line) || ' ';
          if (line.startsWith('+') && !line.startsWith('+++')) {
            return `<div class="bg-emerald-950/60 text-emerald-300 px-2 whitespace-pre">${esc}</div>`;
          }
          if (line.startsWith('-') && !line.startsWith('---')) {
            return `<div class="bg-rose-950/60 text-rose-300 px-2 whitespace-pre">${esc}</div>`;
          }
          if (line.startsWith('@@')) {
            return `<div class="bg-sky-950/50 text-sky-300 px-2 font-semibold whitespace-pre">${esc}</div>`;
          }
          return `<div class="text-zinc-400 px-2 whitespace-pre">${esc}</div>`;
        })
        .join('');
      return `<details open class="my-2 rounded-xl border border-zinc-700/80 bg-zinc-950 overflow-hidden"><summary class="px-3 py-1.5 bg-zinc-900 text-xs font-mono text-zinc-300 cursor-pointer select-none">Diff Visual</summary><div class="overflow-x-auto text-[11px] font-mono py-1.5 leading-5">${linesHtml}</div></details>`;
    }
    return `<pre><code class="language-${escapeHtml(lang || '')}">${escapeHtml(text)}</code></pre>`;
  };

  function renderMarkdown(text: string): string {
    return marked.parse(text || '', { async: false, renderer: mdRenderer }) as string;
  }

  async function handleCancelActiveJob() {
    if (!activeJobId) return;
    const jid = activeJobId;
    liveStatus = 'Cancelando execução...';
    try {
      await cancelJob(jid);
    } catch {
      // ignore
    }
  }

  function startElapsedTimer() {
    stopElapsedTimer();
    liveElapsed = 0;
    elapsedTimer = setInterval(() => {
      liveElapsed += 1;
    }, 1000);
  }

  function stopElapsedTimer() {
    if (elapsedTimer) {
      clearInterval(elapsedTimer);
      elapsedTimer = null;
    }
  }

  function clearActiveJob() {
    activeJobId = null;
    activeJobLastSeq = 0;
    localStorage.removeItem('agy_active_job');
    localStorage.removeItem('agy_active_seq');
    if (streamAbort) {
      streamAbort.abort();
      streamAbort = null;
    }
    stopElapsedTimer();
    liveStatus = '';
    approvalNeeded = null;
    isGenerating = false;
  }

  async function streamJobEvents(jobId: string, afterSeq = 0) {
    if (streamAbort) streamAbort.abort();
    const controller = new AbortController();
    streamAbort = controller;

    let jobFinished = false;
    let currentTools: string[] = [];

    if (messages.length === 0 || messages[messages.length - 1].role !== 'agent') {
      messages = [...messages, { role: 'agent', content: '' }];
    }
    const agentIdx = messages.length - 1;

    try {
      const response = await fetch(`/api/jobs/${jobId}/events?after=${afterSeq}`, {
        signal: controller.signal,
        credentials: 'same-origin',
      });

      if (!response.ok || !response.body) {
        throw new Error(`HTTP ${response.status}`);
      }

      const reader = response.body.getReader();
      const decoder = new TextDecoder('utf-8');
      let buffer = '';
      let currentEventId = afterSeq;

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() ?? '';

        for (const rawLine of lines) {
          const line = rawLine.trim();
          if (!line || line.startsWith(':')) continue;

          if (line.startsWith('id: ')) {
            const parsedId = parseInt(line.slice(4), 10);
            if (!Number.isNaN(parsedId)) {
              currentEventId = parsedId;
              activeJobLastSeq = parsedId;
              localStorage.setItem('agy_active_seq', String(parsedId));
            }
            continue;
          }

          if (!line.startsWith('data: ')) continue;
          const dataStr = line.slice(6);
          if (dataStr === '[DONE]') {
            jobFinished = true;
            break;
          }

          try {
            const ev = JSON.parse(dataStr);
            if (ev.type === 'init' && ev.conversation_id) {
              conversationId = ev.conversation_id;
              localStorage.setItem('agy_conv_id', ev.conversation_id);
            } else if (ev.type === 'delta') {
              messages[agentIdx].content += ev.text;
              messages = messages;
              liveStatus = 'Digitando resposta...';
              scrollToBottom();
            } else if (ev.type === 'tool') {
              const tName = ev.name || 'ferramenta';
              if (ev.name && !currentTools.includes(ev.name)) {
                currentTools.push(ev.name);
              }
              const preview = ev.preview ? ` (${String(ev.preview).slice(0, 28)}...)` : '';
              liveStatus = ev.state === 'ACTIVE' ? `Executando: ${tName}${preview}` : `Concluído: ${tName}`;
              scrollToBottom();
            } else if (ev.type === 'approval_needed') {
              approvalNeeded = {
                tool: ev.tool || 'permissão',
                command: ev.command || '',
              };
              liveStatus = 'Aguardando aprovação humana (HITL)...';
              scrollToBottom();
            } else if (ev.type === 'done') {
              jobFinished = true;
              if (ev.conversation_id) {
                conversationId = ev.conversation_id;
                localStorage.setItem('agy_conv_id', ev.conversation_id);
              }
              if (!messages[agentIdx].content.trim()) {
                if (ev.response && String(ev.response).trim()) {
                  messages[agentIdx].content = String(ev.response).trim();
                } else {
                  const tools = (ev.executed_tools && ev.executed_tools.length) ? ev.executed_tools : currentTools;
                  messages[agentIdx].content = tools.length > 0
                    ? `*Instruções executadas no CachyCore com sucesso.*\n\n**Ações executadas:**\n` + tools.map((t: string) => `- \`${t}\``).join('\n')
                    : `*Instrução concluída com sucesso no CachyCore.*`;
                }
                messages = messages;
              }
            } else if (ev.type === 'error') {
              jobFinished = true;
              messages[agentIdx].content += `\n\n> **Erro:** ${ev.error}`;
              messages = messages;
            }
          } catch {
            // ignore malformed JSON chunk
          }
        }
      }
    } catch (err: any) {
      if (err?.name === 'AbortError') return;
      if (document.visibilityState === 'visible' && activeJobId === jobId) {
        liveStatus = 'Reconectando ao stream...';
        setTimeout(() => {
          if (activeJobId === jobId && document.visibilityState === 'visible') {
            streamJobEvents(jobId, activeJobLastSeq);
          }
        }, 1500);
      }
      return;
    }

    if (jobFinished && activeJobId === jobId) {
      if (!messages[agentIdx].content.trim()) {
        messages[agentIdx].content = '*Instrução concluída com sucesso no CachyCore.*';
        messages = messages;
      }
      clearActiveJob();
      scrollToBottom();
    }
  }

  async function handleSubmit(e?: Event) {
    if (e) e.preventDefault();
    const text = userInput.trim();
    if ((!text && attachedFiles.length === 0) || isGenerating) return;

    const displayPrompt = text || `[${attachedFiles.length} arquivo(s) enviado(s)]`;
    const filesToSend = attachedFiles.map((f) => f.path);

    userInput = '';
    attachedFiles = [];
    if (textareaEl) textareaEl.style.height = 'auto';

    messages = [...messages, { role: 'user', content: displayPrompt }];
    isGenerating = true;
    liveStatus = 'Iniciando no CachyCore...';
    startElapsedTimer();
    scrollToBottom();

    try {
      const job = await createJob({
        prompt: text || 'Analise os arquivos anexados.',
        conversation_id: conversationId,
        model: selectedModel,
        permission_mode: permissionMode,
        attached_files: filesToSend.length > 0 ? filesToSend : undefined,
      });

      activeJobId = job.job_id;
      activeJobLastSeq = 0;
      localStorage.setItem('agy_active_job', job.job_id);
      localStorage.setItem('agy_active_seq', '0');

      if (job.conversation_id) {
        conversationId = job.conversation_id;
        localStorage.setItem('agy_conv_id', job.conversation_id);
      }

      await streamJobEvents(job.job_id, 0);
    } catch (err: any) {
      messages = [...messages, { role: 'agent', content: `> **Erro:** ${err?.message || err}` }];
      clearActiveJob();
    }
  }

  async function loadConversation(id: string) {
    clearActiveJob();
    try {
      const data = await fetchConversation(id);
      conversationId = id;
      localStorage.setItem('agy_conv_id', id);
      messages = data.messages || [];
      await scrollToBottom();

      if (data.active_job_id) {
        activeJobId = data.active_job_id;
        activeJobLastSeq = 0;
        isGenerating = true;
        liveStatus = 'Retomando execução...';
        startElapsedTimer();
        await streamJobEvents(data.active_job_id, 0);
      }
    } catch {
      // ignore if deleted
    }
  }

  function newConversation() {
    clearActiveJob();
    conversationId = null;
    localStorage.removeItem('agy_conv_id');
    messages = [];
  }

  async function handleFilesSelected(e: Event) {
    const input = e.target as HTMLInputElement;
    if (!input.files || input.files.length === 0) return;
    uploading = true;
    try {
      for (const file of Array.from(input.files)) {
        const res = await uploadFile(file);
        attachedFiles = [...attachedFiles, { name: res.filename, path: res.path }];
      }
    } finally {
      uploading = false;
      input.value = '';
    }
  }

  function toggleVoice() {
    if (!recognition) return;
    if (isListening) recognition.stop();
    else recognition.start();
  }

  function handleVisibilityChange() {
    if (document.visibilityState === 'visible' && activeJobId && !streamAbort) {
      liveStatus = 'Retomando stream...';
      streamJobEvents(activeJobId, activeJobLastSeq);
    }
  }

  async function refreshStatusAndOutbox() {
    try {
      const st = await fetchStatus();
      targetOnline = st.online;
    } catch {
      targetOnline = false;
    }
    try {
      outboxFiles = await fetchOutbox();
    } catch {
      // ignore
    }
  }

  async function handleWakeOnLan() {
    await triggerWol();
    setTimeout(refreshStatusAndOutbox, 3000);
  }

  let statusInterval: ReturnType<typeof setInterval> | null = null;

  onMount(async () => {
    document.addEventListener('visibilitychange', handleVisibilityChange);

    // Setup Speech Recognition
    const SpeechRec = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition;
    if (SpeechRec) {
      recognition = new SpeechRec();
      recognition.lang = 'pt-BR';
      recognition.continuous = true;
      recognition.interimResults = true;
      recognition.onstart = () => (isListening = true);
      recognition.onend = () => (isListening = false);
      recognition.onerror = () => recognition.stop();
      recognition.onresult = (ev: any) => {
        let transcript = '';
        for (let i = ev.resultIndex; i < ev.results.length; i++) {
          transcript += ev.results[i][0].transcript;
        }
        userInput = transcript;
      };
    }

    // Load settings, models, features, status, outbox in parallel
    const [loadedSettings, loadedModels, loadedFeatures] = await Promise.all([
      fetchSettings().catch(() => settings),
      fetchModels().catch(() => models),
      fetchFeatures().catch(() => null),
      refreshStatusAndOutbox(),
    ]);

    settings = loadedSettings;
    if (loadedModels.length > 0) models = loadedModels;
    if (loadedFeatures?.features) {
      features = loadedFeatures.features;
      if ((loadedFeatures as any).mode) {
        gatewayMode = (loadedFeatures as any).mode;
      }
    }

    selectedModel = loadedSettings.default_model || 'gemini-4-hydrogen-high';
    permissionMode = loadedSettings.default_permission_mode || 'copilot';

    if (conversationId) {
      await loadConversation(conversationId);
    } else if (activeJobId) {
      isGenerating = true;
      liveStatus = 'Retomando sessão ativa...';
      startElapsedTimer();
      await streamJobEvents(activeJobId, activeJobLastSeq);
    }

    statusInterval = setInterval(refreshStatusAndOutbox, 15000);
  });

  onDestroy(() => {
    document.removeEventListener('visibilitychange', handleVisibilityChange);
    if (statusInterval) clearInterval(statusInterval);
    stopElapsedTimer();
    streamAbort?.abort();
  });
</script>

<div class="h-[100dvh] w-full max-w-[100vw] overflow-x-hidden flex flex-col bg-zinc-950 text-zinc-100">
  <!-- Header -->
  <header class="h-13 bg-zinc-900/95 border-b border-zinc-800 px-2.5 flex items-center justify-between gap-2 shrink-0 select-none">
    <div class="flex items-center gap-2 min-w-0">
      <button
        type="button"
        onclick={() => (sidebarOpen = true)}
        class="p-2 rounded-xl text-zinc-300 hover:bg-zinc-800"
        aria-label="Menu"
      >
        <Icon name="menu" size={20} />
      </button>

      <div class="flex items-center gap-1.5 min-w-0">
        <span class="w-2 h-2 rounded-full shrink-0 {targetOnline === true ? 'bg-emerald-500' : targetOnline === false ? 'bg-rose-500' : 'bg-amber-500'}"></span>
        <span class="text-xs font-bold tracking-tight truncate">{gatewayMode === 'standalone' ? 'AGY Local' : 'CachyCore'}</span>
        {#if targetOnline === false && features.wol}
          <button
            type="button"
            onclick={handleWakeOnLan}
            class="px-2 py-0.5 rounded-md bg-amber-500/20 text-amber-300 border border-amber-500/30 text-[10px] font-semibold flex items-center gap-1"
          >
            <Icon name="power" size={11} />
            <span>Acordar</span>
          </button>
        {/if}
      </div>
    </div>

    <div class="flex items-center gap-1.5 shrink-0">
      <button
        type="button"
        onclick={() => (outboxOpen = true)}
        class="relative p-2 rounded-xl bg-zinc-800/80 text-zinc-300 hover:text-sky-400"
        title="Outbox"
      >
        <Icon name="outbox" size={18} />
        {#if outboxFiles.length > 0}
          <span class="absolute -top-0.5 -right-0.5 min-w-[16px] h-4 px-1 rounded-full bg-sky-500 text-zinc-950 text-[10px] font-bold flex items-center justify-center">
            {outboxFiles.length}
          </span>
        {/if}
      </button>

      {#if features.terminal}
        <button
          type="button"
          onclick={() => (terminalOpen = true)}
          class="p-2 rounded-xl bg-zinc-800/80 text-zinc-300 hover:text-emerald-400"
          title="Terminal HITL"
        >
          <Icon name="terminal" size={18} />
        </button>
      {/if}

      <button
        type="button"
        onclick={newConversation}
        class="p-2 rounded-xl bg-sky-600/20 text-sky-400 border border-sky-500/30 hover:bg-sky-600/30"
        title="Nova Conversa"
      >
        <Icon name="plus" size={18} />
      </button>
    </div>
  </header>

  <!-- Chat Area -->
  <main bind:this={chatContainer} class="flex-1 overflow-y-auto px-3 py-4 space-y-3 overscroll-contain">
    {#if messages.length === 0}
      <div class="flex flex-col items-center justify-center h-full text-center px-4 py-8 text-zinc-500 select-none">
        <img src="/static/icon.svg" class="w-14 h-14 rounded-2xl mb-3 shadow-lg" alt="AGY Logo" />
        <h1 class="text-sm font-bold text-zinc-200">Antigravity Mobile Gateway</h1>
        <p class="text-xs text-zinc-400 mt-1 max-w-xs">
          Svelte 5 + SSE resumível. Suporta Gemini 4 Hydrogen, Extra, Ollama local, Inbox/Outbox e Terminal SSH nativo.
        </p>
      </div>
    {:else}
      {#each messages as msg, i}
        <div class="flex {msg.role === 'user' ? 'justify-end' : 'justify-start'} min-w-0">
          {#if msg.role === 'user'}
            <div class="max-w-[85%] rounded-2xl rounded-br-sm bg-sky-600 text-white px-3.5 py-2.5 text-sm shadow-sm whitespace-pre-wrap break-words">
              {msg.content}
            </div>
          {:else}
            <div
              class="max-w-[94%] rounded-2xl rounded-bl-sm bg-zinc-900 border border-zinc-800/90 text-zinc-100 px-3.5 py-2.5 shadow-sm markdown-body {isGenerating && i === messages.length - 1 ? 'typing-cursor' : ''}"
            >
              {@html renderMarkdown(msg.content)}
            </div>
          {/if}
        </div>
      {/each}
    {/if}
  </main>

  <!-- Interactive HITL Approval Card -->
  {#if approvalNeeded}
    <div class="mx-2.5 mb-2 p-3 rounded-xl bg-amber-950/50 border border-amber-600/50 text-amber-100 space-y-2 shrink-0">
      <div class="flex items-center justify-between gap-2 text-xs font-semibold text-amber-300">
        <div class="flex items-center gap-1.5">
          <Icon name="shield" size={15} />
          <span>Aprovação HITL Necessária ({approvalNeeded.tool})</span>
        </div>
      </div>
      {#if approvalNeeded.command}
        <pre class="bg-zinc-950/90 border border-zinc-800 rounded-lg p-2 text-[11px] font-mono text-zinc-200 overflow-x-auto">{approvalNeeded.command}</pre>
      {/if}
      <div class="flex items-center gap-2">
        {#if features.terminal}
          <button
            type="button"
            onclick={async () => {
              if (approvalNeeded?.command) {
                await navigator.clipboard.writeText(approvalNeeded.command).catch(() => {});
              }
              terminalOpen = true;
            }}
            class="flex-1 py-1.5 px-2.5 rounded-lg bg-emerald-600 hover:bg-emerald-500 text-white text-xs font-semibold flex items-center justify-center gap-1.5"
          >
            <Icon name="terminal" size={14} />
            <span>Copiar & Abrir Terminal</span>
          </button>
        {/if}
        <button
          type="button"
          onclick={handleCancelActiveJob}
          class="py-1.5 px-3 rounded-lg bg-rose-600/30 hover:bg-rose-600/40 text-rose-200 border border-rose-500/40 text-xs font-semibold"
        >
          Rejeitar
        </button>
      </div>
    </div>
  {/if}

  <!-- Live Status Bar -->
  {#if isGenerating && liveStatus}
    <div class="px-3 py-1.5 bg-zinc-900/90 border-t border-zinc-800/80 flex items-center justify-between text-[11px] text-sky-400 font-mono shrink-0">
      <div class="flex items-center gap-2 min-w-0">
        <span class="w-2 h-2 rounded-full bg-sky-400 animate-pulse shrink-0"></span>
        <span class="truncate">{liveStatus}</span>
      </div>
      <div class="flex items-center gap-2 shrink-0">
        <span class="text-zinc-500">{liveElapsed}s</span>
        <button
          type="button"
          onclick={handleCancelActiveJob}
          class="px-2 py-0.5 rounded bg-rose-600/20 text-rose-300 border border-rose-500/40 hover:bg-rose-600/30 text-[10px] font-semibold flex items-center gap-1"
        >
          <Icon name="stop" size={10} />
          <span>Cancelar</span>
        </button>
      </div>
    </div>
  {/if}

  <!-- Input Area -->
  <footer class="bg-zinc-900/95 border-t border-zinc-800 p-2.5 space-y-2 shrink-0">
    <!-- Model & Permission Selector Row -->
    <div class="flex items-center gap-1.5 overflow-x-auto pb-0.5">
      <select
        bind:value={selectedModel}
        class="flex-1 min-w-0 bg-zinc-950 border border-zinc-800 rounded-lg px-2 py-1 text-[11px] text-zinc-200 focus:outline-none focus:border-sky-500 truncate"
      >
        {#each models as m}
          <option value={m.id}>{m.name} ({m.badge})</option>
        {/each}
      </select>

      <select
        bind:value={permissionMode}
        class="bg-zinc-950 border border-zinc-800 rounded-lg px-2 py-1 text-[11px] font-medium focus:outline-none focus:border-sky-500 shrink-0 {permissionMode === 'autonomous' ? 'text-amber-300 border-amber-700/50' : permissionMode === 'readonly' ? 'text-sky-300' : 'text-emerald-300'}"
      >
        <option value="copilot">Copiloto</option>
        <option value="autonomous">Autônomo</option>
        <option value="readonly">Leitura</option>
      </select>
    </div>

    <!-- Attached File Chips -->
    {#if attachedFiles.length > 0}
      <div class="flex flex-wrap gap-1.5">
        {#each attachedFiles as f, idx}
          <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-sky-950/60 border border-sky-700/50 text-[11px] text-sky-300">
            <Icon name="file" size={11} />
            <span class="max-w-[130px] truncate">{f.name}</span>
            <button
              type="button"
              onclick={() => (attachedFiles = attachedFiles.filter((_, k) => k !== idx))}
              class="text-zinc-400 hover:text-rose-400"
            >
              <Icon name="close" size={11} />
            </button>
          </span>
        {/each}
      </div>
    {/if}

    <!-- Slash Commands Palette -->
    {#if slashMenuOpen || userInput.startsWith('/')}
      <div class="bg-zinc-950 border border-zinc-800 rounded-xl p-1.5 space-y-1 max-h-48 overflow-y-auto">
        {#each slashCommands.filter((c) => !userInput.startsWith('/') || c.cmd.startsWith(userInput.trim().toLowerCase())) as item}
          <button
            type="button"
            onclick={() => runSlashCommand(item.cmd)}
            class="w-full text-left px-2.5 py-1.5 rounded-lg hover:bg-zinc-800/80 flex items-center justify-between gap-2 text-xs"
          >
            <span class="font-mono font-semibold text-sky-400">{item.cmd}</span>
            <span class="text-zinc-400 text-[11px] truncate">{item.desc}</span>
          </button>
        {/each}
      </div>
    {/if}

    <!-- Quick Actions Flex Row (/ + Clipe + Mic) -->
    <div class="flex items-center gap-1.5">
      <button
        type="button"
        onclick={() => (slashMenuOpen = !slashMenuOpen)}
        class="px-2.5 py-1.5 rounded-lg text-xs font-mono font-semibold flex items-center gap-1 transition-colors {slashMenuOpen ? 'bg-sky-600 text-white' : 'bg-zinc-800/90 text-sky-400 hover:bg-zinc-700'}"
        title="Comandos Slash (/)"
      >
        <span>/</span>
        <span class="font-sans text-[11px] font-medium">Comandos</span>
      </button>

      <input
        bind:this={fileInputEl}
        type="file"
        multiple
        onchange={handleFilesSelected}
        class="hidden"
      />
      <button
        type="button"
        onclick={() => fileInputEl?.click()}
        disabled={uploading}
        class="px-2.5 py-1.5 rounded-lg bg-zinc-800/90 text-zinc-300 hover:text-sky-400 text-[11px] font-medium flex items-center gap-1"
        title="Anexar arquivo ou foto"
      >
        <Icon name="paperclip" size={14} />
        <span>Anexar</span>
      </button>

      {#if recognition}
        <button
          type="button"
          onclick={toggleVoice}
          class="px-2.5 py-1.5 rounded-lg text-[11px] font-medium flex items-center gap-1 transition-colors {isListening ? 'bg-rose-600 text-white animate-pulse' : 'bg-zinc-800/90 text-zinc-300'}"
          title="Ditado por voz"
        >
          <Icon name="mic" size={14} />
          <span>{isListening ? 'Ouvindo...' : 'Voz'}</span>
        </button>
      {/if}
    </div>

    <!-- Main Form (Full-width Textarea + Send) -->
    <form onsubmit={handleSubmit} class="flex items-end gap-1.5">
      <textarea
        bind:this={textareaEl}
        bind:value={userInput}
        rows={1}
        placeholder={uploading ? 'Enviando arquivo...' : 'Instrução para o CachyCore...'}
        oninput={(e) => {
          const el = e.currentTarget;
          el.style.height = 'auto';
          el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
        }}
        onkeydown={(e) => {
          if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault();
            handleSubmit();
          }
        }}
        class="flex-1 bg-zinc-950 border border-zinc-800 rounded-xl px-3 py-2 text-sm text-zinc-100 placeholder-zinc-500 focus:outline-none focus:border-sky-500 resize-none max-h-[120px]"
      ></textarea>

      {#if isGenerating && activeJobId}
        <button
          type="button"
          onclick={handleCancelActiveJob}
          class="p-2.5 rounded-xl bg-rose-600 hover:bg-rose-500 text-white shrink-0 transition-all"
          title="Cancelar execução (Esc)"
          aria-label="Cancelar"
        >
          <Icon name="stop" size={18} />
        </button>
      {:else}
        <button
          type="submit"
          disabled={isGenerating || (!userInput.trim() && attachedFiles.length === 0)}
          class="p-2.5 rounded-xl bg-sky-600 hover:bg-sky-500 disabled:opacity-40 text-white shrink-0 transition-all"
          aria-label="Enviar"
        >
          <Icon name="send" size={18} />
        </button>
      {/if}
    </form>
  </footer>
</div>

<Sidebar
  bind:open={sidebarOpen}
  activeConvId={conversationId}
  {models}
  watchdogEnabled={features.watchdog !== false}
  telemetryEnabled={features.telemetry === true}
  bind:settings={settings}
  onSelectConversation={loadConversation}
  onNewConversation={newConversation}
  onSettingsSaved={(s) => {
    permissionMode = s.default_permission_mode;
    selectedModel = s.default_model;
  }}
/>

<TerminalModal bind:open={terminalOpen} />
<OutboxModal bind:open={outboxOpen} bind:files={outboxFiles} />

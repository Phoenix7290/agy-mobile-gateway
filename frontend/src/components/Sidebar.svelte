<script lang="ts">
  import Icon from './Icon.svelte';
  import {
    deleteConversation,
    fetchConversations,
    fetchTelemetry,
    fetchWatchdog,
    updateSettings,
    type ConversationSummary,
    type GatewaySettings,
    type ModelInfo,
    type PermissionMode,
    type TelemetryData,
    type WatchdogState,
  } from '../lib/api';

  let {
    open = $bindable(false),
    activeConvId,
    models,
    watchdogEnabled = true,
    telemetryEnabled = false,
    settings = $bindable<GatewaySettings>(),
    onSelectConversation,
    onNewConversation,
    onSettingsSaved,
  }: {
    open: boolean;
    activeConvId: string | null;
    models: ModelInfo[];
    watchdogEnabled?: boolean;
    telemetryEnabled?: boolean;
    settings: GatewaySettings;
    onSelectConversation: (id: string) => void;
    onNewConversation: () => void;
    onSettingsSaved: (s: GatewaySettings) => void;
  } = $props();

  let activeTab = $state<'chats' | 'settings'>('chats');
  let conversations = $state<ConversationSummary[]>([]);
  let searchQuery = $state('');
  let loading = $state(false);
  let hasMore = $state(false);
  let offset = $state(0);
  let savingSettings = $state(false);
  let settingsSavedMsg = $state('');
  let watchdog = $state<WatchdogState | null>(null);
  let telemetry = $state<TelemetryData | null>(null);

  const PAGE_SIZE = 30;

  async function load(reset = true) {
    loading = true;
    try {
      const nextOffset = reset ? 0 : offset;
      const data = await fetchConversations(searchQuery, PAGE_SIZE, nextOffset);
      if (reset) {
        conversations = data.conversations;
      } else {
        conversations = [...conversations, ...data.conversations];
      }
      hasMore = data.has_more;
      offset = nextOffset + data.conversations.length;
    } finally {
      loading = false;
    }
  }

  let searchTimer: ReturnType<typeof setTimeout> | null = null;
  function handleSearchInput() {
    if (searchTimer) clearTimeout(searchTimer);
    searchTimer = setTimeout(() => load(true), 250);
  }

  async function handleDelete(e: MouseEvent, id: string) {
    e.stopPropagation();
    await deleteConversation(id);
    await load(true);
  }

  async function saveSettingsPatch(patch: Partial<GatewaySettings>) {
    savingSettings = true;
    settingsSavedMsg = '';
    try {
      const updated = await updateSettings(patch);
      settings = updated;
      onSettingsSaved(updated);
      settingsSavedMsg = 'Salvo permanentemente';
      setTimeout(() => (settingsSavedMsg = ''), 2000);
    } finally {
      savingSettings = false;
    }
  }

  $effect(() => {
    if (open && activeTab === 'chats') {
      load(true);
    } else if (open && activeTab === 'settings') {
      if (watchdogEnabled) {
        fetchWatchdog().then((w) => (watchdog = w)).catch(() => {});
      }
      if (telemetryEnabled) {
        fetchTelemetry().then((t) => (telemetry = t)).catch(() => {});
      }
    }
  });

  const permOptions: { value: PermissionMode; label: string; desc: string; icon: 'shield' | 'zap' | 'eye' }[] = [
    { value: 'copilot', label: 'Copiloto', desc: 'Aprova edições (--mode accept-edits)', icon: 'shield' },
    { value: 'autonomous', label: 'Autônomo', desc: 'Execução total sem bloqueios', icon: 'zap' },
    { value: 'readonly', label: 'Leitura', desc: 'Somente planejamento (--mode plan)', icon: 'eye' },
  ];
</script>

{#if open}
  <!-- Backdrop -->
  <button
    type="button"
    aria-label="Fechar menu"
    onclick={() => (open = false)}
    class="fixed inset-0 bg-black/60 backdrop-blur-sm z-40"
  ></button>

  <!-- Drawer -->
  <aside class="fixed inset-y-0 left-0 w-80 max-w-[88vw] bg-zinc-900 border-r border-zinc-800 z-50 flex flex-col shadow-2xl select-none">
    <!-- Header -->
    <div class="p-3.5 border-b border-zinc-800 flex items-center justify-between">
      <div class="flex items-center gap-2">
        <Icon name="clock" size={17} class="text-sky-400" />
        <h2 class="text-sm font-bold text-zinc-100">AGY Gateway</h2>
      </div>
      <button type="button" onclick={() => (open = false)} class="p-1 rounded-lg text-zinc-400 hover:text-zinc-200">
        <Icon name="close" size={18} />
      </button>
    </div>

    <!-- Tabs -->
    <div class="grid grid-cols-2 border-b border-zinc-800 bg-zinc-950/60 p-1 gap-1">
      <button
        type="button"
        onclick={() => (activeTab = 'chats')}
        class="py-1.5 rounded-lg text-xs font-semibold flex items-center justify-center gap-1.5 transition-colors {activeTab === 'chats' ? 'bg-zinc-800 text-zinc-100' : 'text-zinc-400 hover:text-zinc-200'}"
      >
        <Icon name="clock" size={14} />
        <span>Sessões</span>
      </button>
      <button
        type="button"
        onclick={() => (activeTab = 'settings')}
        class="py-1.5 rounded-lg text-xs font-semibold flex items-center justify-center gap-1.5 transition-colors {activeTab === 'settings' ? 'bg-zinc-800 text-zinc-100' : 'text-zinc-400 hover:text-zinc-200'}"
      >
        <Icon name="settings" size={14} />
        <span>Configs</span>
      </button>
    </div>

    {#if activeTab === 'chats'}
      <div class="p-3 border-b border-zinc-800/60 space-y-2">
        <button
          type="button"
          onclick={() => {
            onNewConversation();
            open = false;
          }}
          class="w-full py-2 px-3 rounded-xl bg-sky-600 hover:bg-sky-500 text-white text-xs font-semibold flex items-center justify-center gap-1.5 shadow-sm"
        >
          <Icon name="plus" size={15} />
          <span>Nova Conversa</span>
        </button>

        <div class="relative flex items-center">
          <Icon name="search" size={14} class="absolute left-2.5 text-zinc-500 pointer-events-none" />
          <input
            type="text"
            bind:value={searchQuery}
            oninput={handleSearchInput}
            placeholder="Buscar (FTS5)..."
            class="w-full bg-zinc-950 border border-zinc-800 rounded-lg pl-8 pr-2.5 py-1.5 text-xs text-zinc-100 placeholder-zinc-500 focus:outline-none focus:border-sky-500"
          />
        </div>
      </div>

      <div class="flex-1 overflow-y-auto p-2 space-y-1.5">
        {#if loading && conversations.length === 0}
          <div class="text-center py-8 text-xs text-zinc-500">Sincronizando sessões...</div>
        {:else if conversations.length === 0}
          <div class="text-center py-8 text-xs text-zinc-500">Nenhuma sessão encontrada.</div>
        {:else}
          {#each conversations as c}
            <div
              role="button"
              tabindex="0"
              onclick={() => {
                onSelectConversation(c.id);
                open = false;
              }}
              onkeydown={(e) => {
                if (e.key === 'Enter') {
                  onSelectConversation(c.id);
                  open = false;
                }
              }}
              class="w-full text-left p-2.5 rounded-xl border transition-colors cursor-pointer flex items-start justify-between gap-2 {activeConvId === c.id ? 'bg-sky-950/40 border-sky-500/40' : 'bg-zinc-950/60 border-zinc-800/80 hover:border-zinc-700'}"
            >
              <div class="min-w-0 flex-1">
                <div class="text-xs font-medium text-zinc-200 truncate">{c.title || 'Sem título'}</div>
                <div class="flex items-center gap-1.5 mt-1 text-[10px] text-zinc-500">
                  <span class="px-1.5 py-0.5 rounded bg-zinc-800 text-zinc-300 font-mono">{c.engine === 'ollama' ? 'Local' : 'AGY'}</span>
                  <span>{c.updated_at ? new Date(c.updated_at).toLocaleDateString('pt-BR', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' }) : ''}</span>
                </div>
              </div>
              {#if c.engine === 'ollama'}
                <button
                  type="button"
                  onclick={(e) => handleDelete(e, c.id)}
                  class="p-1 rounded text-zinc-500 hover:text-rose-400 shrink-0"
                  title="Excluir"
                >
                  <Icon name="trash" size={14} />
                </button>
              {/if}
            </div>
          {/each}

          {#if hasMore}
            <button
              type="button"
              onclick={() => load(false)}
              disabled={loading}
              class="w-full py-2 rounded-lg bg-zinc-800 hover:bg-zinc-700 text-xs text-zinc-300 font-medium"
            >
              {loading ? 'Carregando...' : 'Carregar mais'}
            </button>
          {/if}
        {/if}
      </div>
    {:else}
      <!-- Settings Tab -->
      <div class="flex-1 overflow-y-auto p-3.5 space-y-5">
        <div>
          <label class="block text-xs font-semibold text-zinc-300 mb-2">Modo de Permissão Padrão</label>
          <div class="space-y-2">
            {#each permOptions as opt}
              <button
                type="button"
                onclick={() => saveSettingsPatch({ default_permission_mode: opt.value })}
                class="w-full text-left p-2.5 rounded-xl border transition-all flex items-start gap-2.5 {settings.default_permission_mode === opt.value ? 'bg-sky-950/40 border-sky-500/60 text-zinc-100' : 'bg-zinc-950 border-zinc-800 text-zinc-400 hover:border-zinc-700'}"
              >
                <Icon name={opt.icon} size={16} class={settings.default_permission_mode === opt.value ? 'text-sky-400 mt-0.5' : 'text-zinc-500 mt-0.5'} />
                <div class="min-w-0 flex-1">
                  <div class="text-xs font-semibold flex items-center justify-between">
                    <span>{opt.label}</span>
                    {#if settings.default_permission_mode === opt.value}
                      <Icon name="check" size={14} class="text-sky-400" />
                    {/if}
                  </div>
                  <div class="text-[11px] text-zinc-500 mt-0.5">{opt.desc}</div>
                </div>
              </button>
            {/each}
          </div>
        </div>

        <div>
          <label for="cfg-default-model" class="block text-xs font-semibold text-zinc-300 mb-1.5">Modelo Padrão</label>
          <select
            id="cfg-default-model"
            value={settings.default_model}
            onchange={(e) => saveSettingsPatch({ default_model: (e.target as HTMLSelectElement).value })}
            class="w-full bg-zinc-950 border border-zinc-800 rounded-xl px-3 py-2 text-xs text-zinc-100 focus:outline-none focus:border-sky-500"
          >
            {#each models as m}
              <option value={m.id}>{m.name} ({m.badge})</option>
            {/each}
          </select>
        </div>

        {#if settingsSavedMsg}
          <div class="p-2.5 rounded-xl bg-emerald-950/50 border border-emerald-700/50 text-emerald-300 text-xs flex items-center gap-2">
            <Icon name="check" size={14} />
            <span>{settingsSavedMsg}</span>
          </div>
        {/if}

        <div class="pt-2 border-t border-zinc-800 text-[11px] text-zinc-500 space-y-1.5">
          <p>As configurações ficam salvas no banco SQLite do Gateway no Raspberry Pi e sincronizam automaticamente entre sessões.</p>
          {#if savingSettings}
            <p class="text-sky-400">Salvando...</p>
          {/if}
        </div>

        {#if watchdogEnabled && watchdog}
          <div class="p-3 rounded-xl bg-zinc-950 border border-zinc-800 space-y-1.5">
            <div class="flex items-center justify-between text-xs font-semibold text-zinc-200">
              <span>Watchdog CachyCore (Pi)</span>
              <span class="px-1.5 py-0.5 rounded text-[10px] {watchdog.online ? 'bg-emerald-950 text-emerald-300 border border-emerald-700/50' : 'bg-rose-950 text-rose-300 border border-rose-700/50'}">
                {watchdog.online ? 'Online' : 'Offline'}
              </span>
            </div>
            <div class="text-[11px] text-zinc-500 flex justify-between">
              <span>Verificações: {watchdog.uptime_checks}</span>
              <span>Quedas: {watchdog.downtime_events}</span>
            </div>
            {#if watchdog.last_seen_online}
              <div class="text-[10px] text-zinc-500">
                Visto por último: {new Date(watchdog.last_seen_online).toLocaleTimeString('pt-BR')}
              </div>
            {/if}
          </div>
        {/if}

        {#if telemetryEnabled && telemetry && telemetry.enabled}
          <div class="p-3 rounded-xl bg-zinc-950 border border-zinc-800 space-y-1.5">
            <div class="flex items-center justify-between text-xs font-semibold text-zinc-200">
              <span>Telemetria de Hardware</span>
              <Icon name="cpu" size={14} class="text-sky-400" />
            </div>
            {#if telemetry.gpu}
              <div class="text-[11px] text-zinc-300 flex justify-between">
                <span class="truncate pr-2">{telemetry.gpu.name}</span>
                <span class="font-mono text-sky-400 shrink-0">{telemetry.gpu.temp_c}°C</span>
              </div>
              <div class="text-[11px] text-zinc-500 flex justify-between">
                <span>VRAM</span>
                <span class="font-mono">{telemetry.gpu.vram_used_mb} / {telemetry.gpu.vram_total_mb} MB</span>
              </div>
            {/if}
            {#if telemetry.ram}
              <div class="text-[11px] text-zinc-500 flex justify-between">
                <span>RAM</span>
                <span class="font-mono">{Math.round(telemetry.ram.used_mb / 1024)} / {Math.round(telemetry.ram.total_mb / 1024)} GB</span>
              </div>
            {/if}
            {#if telemetry.cpu_temp !== null}
              <div class="text-[11px] text-zinc-500 flex justify-between">
                <span>CPU Temp</span>
                <span class="font-mono">{telemetry.cpu_temp}°C</span>
              </div>
            {/if}
          </div>
        {/if}
      </div>
    {/if}
  </aside>
{/if}

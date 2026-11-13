<script lang="ts">
  import Icon from './Icon.svelte';
  import { deleteOutboxFile, fetchOutbox, type OutboxFile } from '../lib/api';

  let {
    open = $bindable(false),
    files = $bindable<OutboxFile[]>([]),
  }: {
    open: boolean;
    files: OutboxFile[];
  } = $props();

  let loading = $state(false);

  async function refresh() {
    loading = true;
    try {
      files = await fetchOutbox();
    } finally {
      loading = false;
    }
  }

  async function remove(name: string) {
    await deleteOutboxFile(name);
    await refresh();
  }

  function formatSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(2)} MB`;
  }

  $effect(() => {
    if (open) refresh();
  });
</script>

{#if open}
  <div class="fixed inset-0 bg-black/70 backdrop-blur-sm z-50 flex items-end sm:items-center justify-center p-0 sm:p-4">
    <div class="w-full sm:max-w-md bg-zinc-900 border-t sm:border border-zinc-800 rounded-t-2xl sm:rounded-2xl max-h-[82vh] flex flex-col shadow-2xl">
      <div class="p-4 border-b border-zinc-800 flex items-center justify-between">
        <div class="flex items-center gap-2">
          <Icon name="outbox" size={18} class="text-sky-400" />
          <h2 class="text-sm font-bold text-zinc-100">Outbox (/home/ai/agy-outbox)</h2>
        </div>
        <div class="flex items-center gap-1">
          <button type="button" onclick={refresh} class="p-1.5 rounded-lg text-zinc-400 hover:text-sky-400" title="Atualizar">
            <Icon name="refresh" size={16} />
          </button>
          <button type="button" onclick={() => (open = false)} class="p-1.5 rounded-lg text-zinc-400 hover:text-zinc-200" title="Fechar">
            <Icon name="close" size={18} />
          </button>
        </div>
      </div>

      <div class="p-3 overflow-y-auto flex-1 space-y-2">
        {#if loading}
          <p class="text-xs text-zinc-500 text-center py-8">Carregando arquivos...</p>
        {:else if files.length === 0}
          <p class="text-xs text-zinc-500 text-center py-8">Nenhum arquivo no outbox do CachyCore.</p>
        {:else}
          {#each files as f}
            <div class="flex items-center justify-between gap-2 p-2.5 rounded-xl bg-zinc-950 border border-zinc-800">
              <div class="min-w-0 flex-1">
                <div class="text-xs font-medium text-zinc-200 truncate">{f.name}</div>
                <div class="text-[11px] text-zinc-500">{formatSize(f.size)}</div>
              </div>
              <div class="flex items-center gap-1.5 shrink-0">
                <a
                  href={`/api/outbox/${encodeURIComponent(f.name)}`}
                  download={f.name}
                  class="px-2.5 py-1.5 rounded-lg bg-sky-600/20 text-sky-300 border border-sky-500/30 hover:bg-sky-600/30 text-xs font-medium flex items-center gap-1"
                >
                  <Icon name="download" size={14} />
                  <span>Baixar</span>
                </a>
                <button
                  type="button"
                  onclick={() => remove(f.name)}
                  class="p-1.5 rounded-lg text-zinc-500 hover:text-rose-400 hover:bg-rose-950/40"
                  title="Excluir"
                >
                  <Icon name="trash" size={15} />
                </button>
              </div>
            </div>
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}

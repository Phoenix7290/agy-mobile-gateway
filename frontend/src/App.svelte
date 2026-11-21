<script lang="ts">
  import { onDestroy, onMount, type Component } from "svelte";
  import { fade, fly } from "svelte/transition";
  import { cubicOut } from "svelte/easing";
  import Icon from "./components/Icon.svelte";
  import AiSpinner from "./components/AiSpinner.svelte";
  import ChatPane from "./components/ChatPane.svelte";
  import Sidebar from "./components/Sidebar.svelte";
  import type { PreviewTarget } from "./components/OutboxPreviewModal.svelte";
  import { Button } from "./lib/components/ui/button/index.js";
  import { Card } from "./lib/components/ui/card/index.js";
  import { Badge } from "./lib/components/ui/badge/index.js";
  import { Input } from "./lib/components/ui/input/index.js";
  import {
    deleteConversation,
    enableBrowserWebPush,
    fetchFeatures,
    fetchModels,
    fetchOutbox,
    fetchSettings,
    fetchSlashCommands,
    fetchStatus,
    registerGatewayServiceWorker,
    showBrowserNotification,
    triggerWol,
    updateSettings,
    type AlertEvent,
    type ChatMessage,
    type GatewaySettings,
    type ModelInfo,
    type OutboxFile,
    type PermissionMode,
    type SlashCommandItem,
  } from "./lib/api";
  import {
    THEME_PALETTES,
    applyTheme,
    hasStoredTheme,
    normalizeThemeMode,
    normalizeThemePalette,
    readStoredTheme,
    type ThemeMode,
    type ThemePalette,
  } from "./lib/theme";

  interface TabEntry {
    id: string;
    storageKeyPrefix: string;
    title: string;
    convId: string | null;
    isGenerating: boolean;
  }

  // Theme State (applied immediately to avoid FOUC)
  const initialTheme = readStoredTheme();
  let themeMode = $state<ThemeMode>(initialTheme.mode);
  let themePalette = $state<ThemePalette>(initialTheme.palette);
  let themeMenuOpen = $state(false);
  applyTheme(initialTheme.mode, initialTheme.palette, false);

  let activeSwatch = $derived(
    THEME_PALETTES.find((p) => p.id === themePalette)?.swatch ?? "#0ea5e9",
  );

  function setTheme(mode: ThemeMode, palette: ThemePalette) {
    themeMode = mode;
    themePalette = palette;
    applyTheme(mode, palette, true);
    updateSettings({ theme_mode: mode, theme_palette: palette })
      .then((updated) => {
        settings = updated;
      })
      .catch(() => {});
  }

  // Gateway Shared State
  let targetOnline = $state<boolean | null>(null);
  let activeOS = $state<string>("linux");
  let shellEl: HTMLDivElement | undefined = $state();
  let gatewayMode = $state<"gateway" | "standalone" | "ide-bridge">("gateway");
  let outboxDir = $state("");
  let features = $state<Record<string, any>>({
    terminal: true,
    wol: true,
    watchdog: true,
    ollama: true,
    inbox_outbox: true,
  });
  let models = $state<ModelInfo[]>([
    {
      id: "gemini-4-hydrogen-high",
      name: "Gemini 4 Hydrogen (High)",
      type: "cloud",
      badge: "Hydrogen",
    },
    {
      id: "gemini-4-hydrogen-xhigh",
      name: "Gemini 4 Hydrogen (Extra)",
      type: "cloud",
      badge: "Extra",
    },
    {
      id: "gemini-4-hydrogen-medium",
      name: "Gemini 4 Hydrogen (Medium)",
      type: "cloud",
      badge: "Hydrogen",
    },
  ]);
  let settings = $state<GatewaySettings>({
    default_permission_mode: "copilot",
    default_model: "gemini-4-hydrogen-high",
    default_effort: "",
    theme_mode: initialTheme.mode,
    theme_palette: initialTheme.palette,
  });
  let defaultModel = $state("gemini-4-hydrogen-high");
  let defaultPermission = $state<PermissionMode>("copilot");

  let hostLabel = $derived(
    gatewayMode === "standalone"
      ? "AGY Local"
      : gatewayMode === "ide-bridge"
        ? "IDE Bridge"
        : "CachyCore",
  );

  // Tabs & Desktop Split View State
  let tabCounter = 1;
  let tabs = $state<TabEntry[]>([
    {
      id: "tab-1",
      storageKeyPrefix: "agy_",
      title: "",
      convId: localStorage.getItem("agy_conv_id"),
      isGenerating: false,
    },
  ]);
  let leftTabId = $state("tab-1");
  let rightTabId = $state("tab-1");
  let splitOpen = $state(false);
  let focusedSide = $state<"left" | "right">("left");
  let compareOpen = $state(false);
  let comparePrompt = $state("");
  let paneRefs: Record<string, ChatPane | undefined> = {};

  let focusedTabId = $derived(
    splitOpen && focusedSide === "right" ? rightTabId : leftTabId,
  );
  let focusedConvId = $derived(
    tabs.find((t) => t.id === focusedTabId)?.convId ?? null,
  );
  let anyGenerating = $derived(tabs.some((t) => t.isGenerating));

  function createTab(activate = true): TabEntry {
    if (tabs.length >= 6) {
      return tabs[tabs.length - 1];
    }
    tabCounter += 1;
    const newId = `tab-${tabCounter}`;
    const entry: TabEntry = {
      id: newId,
      storageKeyPrefix: `agy_${newId}_`,
      title: "",
      convId: null,
      isGenerating: false,
    };
    tabs = [...tabs, entry];
    if (activate) {
      if (splitOpen && focusedSide === "right") {
        rightTabId = newId;
      } else {
        leftTabId = newId;
      }
    }
    return entry;
  }

  function handleHeaderNewAction() {
    const curTab = tabs.find((t) => t.id === focusedTabId);
    const snap = paneRefs[focusedTabId]?.getSnapshotContext();
    if (curTab && snap && (snap.messages.length > 0 || curTab.convId)) {
      createTab(true);
    } else {
      paneRefs[focusedTabId]?.newConversation();
    }
  }

  function selectTab(tabId: string) {
    if (!splitOpen) {
      leftTabId = tabId;
      focusedSide = "left";
      return;
    }
    if (focusedSide === "left") {
      if (rightTabId === tabId) {
        rightTabId = leftTabId;
      }
      leftTabId = tabId;
    } else {
      if (leftTabId === tabId) {
        leftTabId = rightTabId;
      }
      rightTabId = tabId;
    }
  }

  function closeTab(e: MouseEvent, tabId: string) {
    e.stopPropagation();
    if (tabs.length <= 1) return;
    const remaining = tabs.filter((t) => t.id !== tabId);
    tabs = remaining;
    if (leftTabId === tabId) {
      leftTabId = remaining[0].id;
    }
    if (rightTabId === tabId) {
      rightTabId =
        remaining.find((t) => t.id !== leftTabId)?.id ?? remaining[0].id;
    }
    if (remaining.length < 2) {
      splitOpen = false;
      compareOpen = false;
      focusedSide = "left";
    }
  }

  function toggleSplitView() {
    if (!splitOpen) {
      if (tabs.length < 2) {
        const second = createTab(false);
        rightTabId = second.id;
      } else if (rightTabId === leftTabId) {
        rightTabId = tabs.find((t) => t.id !== leftTabId)?.id ?? leftTabId;
      }
      splitOpen = true;
    } else {
      splitOpen = false;
      compareOpen = false;
      leftTabId = focusedTabId;
      focusedSide = "left";
    }
  }

  async function handleCompareSubmit(e?: Event) {
    if (e) e.preventDefault();
    const prompt = comparePrompt.trim();
    if (!prompt || !splitOpen) return;
    comparePrompt = "";
    const left = paneRefs[leftTabId];
    const right = paneRefs[rightTabId];
    await Promise.all([
      left?.submitExternalPrompt(prompt),
      right && rightTabId !== leftTabId
        ? right.submitExternalPrompt(prompt)
        : Promise.resolve(),
    ]);
  }

  function handlePaneMetaChange(
    tabId: string,
    title: string,
    convId: string | null,
  ) {
    tabs = tabs.map((t) => (t.id === tabId ? { ...t, title, convId } : t));
  }

  function handlePaneGeneratingChange(tabId: string, generating: boolean) {
    const cur = tabs.find((t) => t.id === tabId);
    if (!cur || cur.isGenerating === generating) return;
    tabs = tabs.map((t) =>
      t.id === tabId ? { ...t, isGenerating: generating } : t,
    );
  }

  function visibilityForTab(
    tabId: string,
  ): "active" | "split-hidden-mobile" | "hidden" {
    if (!splitOpen) {
      return tabId === leftTabId ? "active" : "hidden";
    }
    if (tabId === leftTabId && tabId === rightTabId) {
      return "active";
    }
    if (tabId === leftTabId) {
      return focusedSide === "left" ? "active" : "split-hidden-mobile";
    }
    if (tabId === rightTabId) {
      return focusedSide === "right" ? "active" : "split-hidden-mobile";
    }
    return "hidden";
  }

  function orderForTab(tabId: string): string {
    if (!splitOpen) return "";
    if (tabId === leftTabId) return "md:order-1";
    if (tabId === rightTabId) return "md:order-2";
    return "";
  }

  // Modals & Drawers (lazy-loaded chunks for Vite code splitting)
  let sidebarOpen = $state(false);
  let terminalOpen = $state(false);
  let outboxOpen = $state(false);
  let outboxFiles = $state<OutboxFile[]>([]);
  let outboxLoaded = $state(false);
  let mediaPreviewTarget = $state<PreviewTarget | null>(null);
  let automationsOpen = $state(false);
  let slashModalOpen = $state(false);
  let slashCommands = $state<SlashCommandItem[]>([]);
  let unreadAlerts = $state(0);

  let TerminalModalComp = $state<Component<any> | null>(null);
  let OutboxModalComp = $state<Component<any> | null>(null);
  let OutboxPreviewModalComp = $state<Component<any> | null>(null);
  let SlashCommandsModalComp = $state<Component<any> | null>(null);
  let ShareModalComp = $state<Component<any> | null>(null);
  let AutomationsModalComp = $state<Component<any> | null>(null);

  $effect(() => {
    if (terminalOpen && !TerminalModalComp) {
      import("./components/TerminalModal.svelte").then((m) => { TerminalModalComp = m.default; });
    }
    if (outboxOpen && !OutboxModalComp) {
      import("./components/OutboxModal.svelte").then((m) => { OutboxModalComp = m.default; });
    }
    if (mediaPreviewTarget && !OutboxPreviewModalComp) {
      import("./components/OutboxPreviewModal.svelte").then((m) => { OutboxPreviewModalComp = m.default; });
    }
    if (slashModalOpen && !SlashCommandsModalComp) {
      import("./components/SlashCommandsModal.svelte").then((m) => { SlashCommandsModalComp = m.default; });
    }
    if (shareOpen && !ShareModalComp) {
      import("./components/ShareModal.svelte").then((m) => { ShareModalComp = m.default; });
    }
    if (automationsOpen && !AutomationsModalComp) {
      import("./components/AutomationsModal.svelte").then((m) => { AutomationsModalComp = m.default; });
    }
  });

  // Share Snapshot Modal
  let shareOpen = $state(false);
  let shareTitle = $state("Snapshot AGY");
  let shareSingleMD = $state("");
  let shareMessages = $state<ChatMessage[]>([]);

  function openShareModal(
    title: string,
    singleMarkdown: string,
    msgs: ChatMessage[],
  ) {
    shareTitle = title || "Snapshot AGY";
    shareSingleMD = singleMarkdown;
    shareMessages = msgs;
    shareOpen = true;
  }

  // Delete Chat Confirmation State
  let pendingDelete = $state<{ id: string; title: string } | null>(null);
  let deletingChat = $state(false);
  let deleteChatError = $state("");

  function requestDeleteConversation(convId: string, title: string) {
    deleteChatError = "";
    pendingDelete = { id: convId, title: title || "Conversa atual" };
  }

  function handleConversationDeleted(deletedId: string) {
    for (const tab of tabs) {
      if (tab.convId === deletedId) {
        paneRefs[tab.id]?.newConversation();
      }
    }
  }

  async function confirmDeleteChat() {
    if (!pendingDelete || deletingChat) return;
    const targetId = pendingDelete.id;
    deletingChat = true;
    deleteChatError = "";
    try {
      await deleteConversation(targetId);
      pendingDelete = null;
      handleConversationDeleted(targetId);
    } catch (err: any) {
      deleteChatError = err?.message || "Erro ao apagar chat";
    } finally {
      deletingChat = false;
    }
  }

  async function refreshStatusAndOutbox() {
    try {
      const st = await fetchStatus();
      targetOnline = st.online;
      if (typeof st.unread_alerts === "number") {
        unreadAlerts = st.unread_alerts;
      }
      if (st.active_os) activeOS = st.active_os;
    } catch {
      targetOnline = false;
    }
    try {
      outboxFiles = await fetchOutbox();
      outboxLoaded = true;
    } catch {
      // ignore
    }
  }

  async function handleWakeOnLan() {
    await triggerWol();
    setTimeout(refreshStatusAndOutbox, 3000);
  }

  interface BeforeInstallPromptEvent extends Event {
    prompt: () => Promise<void>;
    userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
  }

  let statusInterval: ReturnType<typeof setInterval> | null = null;
  let alertEventSource: EventSource | null = null;
  let liveAlertToast = $state<AlertEvent | null>(null);
  let alertToastTimer: ReturnType<typeof setTimeout> | null = null;
  let deferredInstallPrompt = $state<BeforeInstallPromptEvent | null>(null);
  let browserNotifPerm = $state<NotificationPermission>(
    typeof window !== "undefined" && "Notification" in window
      ? Notification.permission
      : "denied",
  );

  async function handleInstallPWA(): Promise<boolean> {
    if (!deferredInstallPrompt) return false;
    try {
      await deferredInstallPrompt.prompt();
      const choice = await deferredInstallPrompt.userChoice;
      if (choice.outcome === "accepted") {
        deferredInstallPrompt = null;
        return true;
      }
      return false;
    } catch {
      return false;
    }
  }

  function refreshBrowserNotifPerm() {
    if (typeof window !== "undefined" && "Notification" in window) {
      browserNotifPerm = Notification.permission;
    }
  }

  async function handleEnableBrowserFromToast() {
    const current = liveAlertToast;
    try {
      await enableBrowserWebPush();
      refreshBrowserNotifPerm();
      if (current) {
        showBrowserNotification(current, (convId) => {
          paneRefs[focusedTabId]?.loadConversation(convId, current.title);
        });
      }
    } catch {
      refreshBrowserNotifPerm();
    }
  }

  function showLiveAlertToast(ev: AlertEvent) {
    liveAlertToast = ev;
    refreshBrowserNotifPerm();
    if (alertToastTimer) clearTimeout(alertToastTimer);
    alertToastTimer = setTimeout(() => {
      if (liveAlertToast?.id === ev.id) liveAlertToast = null;
    }, 7000);
  }

  function startAlertStream() {
    if (typeof EventSource === "undefined") return;
    if (alertEventSource) alertEventSource.close();
    const es = new EventSource("/api/alerts/stream");
    es.addEventListener("init", (msg) => {
      try {
        const d = JSON.parse((msg as MessageEvent).data);
        if (typeof d.unread_count === "number") unreadAlerts = d.unread_count;
      } catch {
        // ignore
      }
    });
    es.addEventListener("alert", (msg) => {
      try {
        const ev = JSON.parse((msg as MessageEvent).data) as AlertEvent;
        unreadAlerts += 1;
        showLiveAlertToast(ev);
        showBrowserNotification(ev, (convId) => {
          paneRefs[focusedTabId]?.loadConversation(convId, ev.title);
        });
      } catch {
        // ignore
      }
    });
    alertEventSource = es;
  }

  function handleBeforeInstall(e: Event) {
    e.preventDefault();
    deferredInstallPrompt = e as BeforeInstallPromptEvent;
  }

  function handleAppInstalled() {
    deferredInstallPrompt = null;
  }

  onMount(() => {
    window.addEventListener("beforeinstallprompt", handleBeforeInstall);
    window.addEventListener("appinstalled", handleAppInstalled);
    void registerGatewayServiceWorker();

    const syncViewport = () => {
      if (!shellEl) return;
      const vv = window.visualViewport;
      if (vv && vv.height < window.innerHeight - 80) {
        shellEl.style.bottom = 'auto';
        shellEl.style.height = `${vv.height}px`;
        shellEl.style.top = `${vv.offsetTop}px`;
      } else {
        shellEl.style.top = '0px';
        shellEl.style.bottom = '0px';
        shellEl.style.height = '';
      }
    };
    syncViewport();
    window.visualViewport?.addEventListener("resize", syncViewport);
    window.visualViewport?.addEventListener("scroll", syncViewport);

    void fetchSlashCommands()
      .then((cmds) => {
        slashCommands = cmds;
      })
      .catch(() => {});

    void (async () => {
      const [loadedSettings, loadedModels, loadedFeatures] = await Promise.all([
        fetchSettings().catch(() => settings),
        fetchModels().catch(() => models),
        fetchFeatures().catch(() => null),
        refreshStatusAndOutbox(),
      ]);

      settings = loadedSettings;
      if (
        !hasStoredTheme() &&
        (loadedSettings.theme_mode || loadedSettings.theme_palette)
      ) {
        const m = normalizeThemeMode(loadedSettings.theme_mode);
        const p = normalizeThemePalette(loadedSettings.theme_palette);
        themeMode = m;
        themePalette = p;
        applyTheme(m, p, true);
      }

      if (loadedModels.length > 0) models = loadedModels;
      if (loadedFeatures?.features) {
        features = loadedFeatures.features;
        if (
          loadedFeatures.mode === "standalone" ||
          loadedFeatures.mode === "gateway" ||
          loadedFeatures.mode === "ide-bridge"
        ) {
          gatewayMode = loadedFeatures.mode;
        }
        if (loadedFeatures.dirs?.outbox) {
          outboxDir = loadedFeatures.dirs.outbox;
        }
      }

      defaultModel = loadedSettings.default_model || "gemini-4-hydrogen-high";
      defaultPermission = loadedSettings.default_permission_mode || "copilot";

      const convParam = new URLSearchParams(window.location.search).get("conv");
      if (convParam) {
        paneRefs[focusedTabId]?.loadConversation(convParam);
        window.history.replaceState({}, "", "/");
      }

      startAlertStream();
      statusInterval = setInterval(refreshStatusAndOutbox, 15000);
    })();

    return () => {
      window.visualViewport?.removeEventListener("resize", syncViewport);
      window.visualViewport?.removeEventListener("scroll", syncViewport);
    };
  });

  onDestroy(() => {
    if (statusInterval) clearInterval(statusInterval);
    if (alertToastTimer) clearTimeout(alertToastTimer);
    if (alertEventSource) alertEventSource.close();
    if (typeof window !== "undefined") {
      window.removeEventListener("beforeinstallprompt", handleBeforeInstall);
      window.removeEventListener("appinstalled", handleAppInstalled);
    }
  });
</script>

<svelte:window
  onkeydown={(e) => {
    if (e.key === "Escape") {
      if (themeMenuOpen) themeMenuOpen = false;
      if (pendingDelete && !deletingChat) pendingDelete = null;
      if (automationsOpen) automationsOpen = false;
    }
  }}
/>

<div
  bind:this={shellEl}
  style="will-change: transform, height;"
  class="fixed inset-0 w-full max-w-[100vw] overflow-x-hidden flex flex-col bg-[var(--bg-app)] text-[var(--text-primary)] theme-surface"
>
  <!-- Header -->
  <header
    class="relative h-[calc(3.25rem+env(safe-area-inset-top))] pt-[env(safe-area-inset-top)] bg-background/85 backdrop-blur-xl border-b border-border px-2.5 sm:px-5 flex items-center justify-between gap-1.5 shrink-0 select-none z-30 theme-surface"
  >
    <div class="flex items-center gap-2 min-w-0">
      <Button
        variant="ghost"
        size="icon-sm"
        onclick={() => (sidebarOpen = true)}
        aria-label="Menu"
      >
        <Icon name="menu" size={19} />
      </Button>

      <div class="flex items-center gap-1.5 min-w-0">
        {#if anyGenerating}
          <AiSpinner size={14} class="text-accent-foreground" />
        {:else}
          <span
            class="w-2 h-2 rounded-full shrink-0 transition-colors {targetOnline ===
            true
              ? 'bg-emerald-500 shadow-[0_0_8px_rgba(16,185,129,0.5)]'
              : targetOnline === false
                ? 'bg-rose-500'
                : 'bg-amber-500'}"
          ></span>
        {/if}
        <span class="text-xs font-bold tracking-tight truncate text-foreground"
          >{hostLabel}</span
        >
        {#if targetOnline === true && features.dual_boot}
          <Badge variant="accent" size="xs" class="font-mono shrink-0">
            {activeOS === "windows" ? "Win" : "Linux"}
          </Badge>
        {/if}
        {#if targetOnline === false && features.wol}
          <Button
            variant="outline"
            size="xs"
            onclick={handleWakeOnLan}
            class="border-amber-500/35 bg-amber-500/12 text-amber-500 hover:bg-amber-500/20"
          >
            <Icon name="power" size={11} />
            <span>Acordar</span>
          </Button>
        {/if}
      </div>
    </div>

    <div class="flex items-center gap-1 sm:gap-1.5 shrink-0">
      <!-- Desktop Split View Toggle -->
      <Button
        variant={splitOpen ? "outline" : "ghost"}
        size="icon-sm"
        onclick={toggleSplitView}
        class="hidden md:inline-flex {splitOpen
          ? 'bg-accent border-[var(--accent-border)] text-accent-foreground'
          : ''}"
        title={splitOpen
          ? "Sair do Split View"
          : "Split View (2 sessões lado a lado)"}
        aria-label="Alternar Split View"
      >
        <Icon name="columns" size={16} />
      </Button>

      <!-- Share Active Conversation -->
      <Button
        variant="ghost"
        size="icon-sm"
        onclick={() => {
          const ctx = paneRefs[focusedTabId]?.getSnapshotContext();
          if (ctx && ctx.messages.length > 0) {
            openShareModal(ctx.title, "", ctx.messages);
          }
        }}
        class="hidden sm:inline-flex"
        title="Compartilhar / Exportar conversa"
        aria-label="Compartilhar conversa"
      >
        <Icon name="share" size={16} />
      </Button>

      <!-- Delete Active Conversation -->
      {#if focusedConvId}
        <Button
          variant="ghost"
          size="icon-sm"
          onclick={() => {
            const focusedTab = tabs.find((t) => t.id === focusedTabId);
            const ctx = paneRefs[focusedTabId]?.getSnapshotContext();
            requestDeleteConversation(
              focusedConvId,
              ctx?.title || focusedTab?.title || "Conversa atual",
            );
          }}
          class="hover:text-rose-400 hover:bg-rose-500/10"
          title="Apagar chat atual"
          aria-label="Apagar chat atual"
        >
          <Icon name="trash" size={16} />
        </Button>
      {/if}

      <!-- Automations & Active Alerts Bell -->
      <Button
        variant="ghost"
        size="icon-sm"
        onclick={() => (automationsOpen = true)}
        class="relative !overflow-visible"
        title="Automações Cron & Alertas Ativos"
        aria-label="Automações Cron e Alertas"
      >
        <Icon name="bell" size={16} />
        {#if unreadAlerts > 0}
          <span
            class="absolute -top-0.5 -right-0.5 min-w-[16px] h-4 px-1 rounded-full bg-rose-600 text-white text-[10px] font-bold flex items-center justify-center shadow-xs"
          >
            {unreadAlerts > 99 ? "99+" : unreadAlerts}
          </span>
        {/if}
      </Button>

      <!-- Theme & Color Palette Button -->
      <Button
        variant={themeMenuOpen ? "outline" : "ghost"}
        size="icon-sm"
        onclick={() => (themeMenuOpen = !themeMenuOpen)}
        class="relative !overflow-visible {themeMenuOpen
          ? 'bg-accent border-[var(--accent-border)] text-accent-foreground'
          : ''}"
        title="Tema e paleta de cores"
        aria-label="Tema e paleta de cores"
      >
        <Icon name={themeMode === "dark" ? "moon" : "sun"} size={16} />
        <span
          class="absolute bottom-1 right-1 w-2 h-2 rounded-full ring-1 ring-background"
          style="background-color: {activeSwatch};"
        ></span>
      </Button>

      <Button
        variant="ghost"
        size="icon-sm"
        onclick={() => (outboxOpen = true)}
        class="relative !overflow-visible"
        title="Outbox"
      >
        <Icon name="outbox" size={17} />
        {#if outboxFiles.length > 0}
          <span
            class="absolute -top-0.5 -right-0.5 min-w-[16px] h-4 px-1 rounded-full bg-primary text-primary-foreground text-[10px] font-bold flex items-center justify-center shadow-xs"
          >
            {outboxFiles.length}
          </span>
        {/if}
      </Button>

      {#if features.terminal}
        <Button
          variant="ghost"
          size="icon-sm"
          onclick={() => (terminalOpen = true)}
          class="hover:text-emerald-500"
          title="Terminal HITL"
        >
          <Icon name="terminal" size={17} />
        </Button>
      {/if}

      <Button
        variant="default"
        size="icon-sm"
        onclick={handleHeaderNewAction}
        title="Nova Aba / Conversa"
        aria-label="Nova Aba ou Conversa"
      >
        <Icon name="plus" size={17} />
      </Button>
    </div>

    <!-- Quick Theme & Palette Popover -->
    {#if themeMenuOpen}
      <button
        type="button"
        aria-label="Fechar seletor de tema"
        onclick={() => (themeMenuOpen = false)}
        class="fixed inset-0 z-30 cursor-default"
      ></button>

      <div
        transition:fly={{ y: -8, duration: 160, easing: cubicOut }}
        class="absolute right-2.5 top-14 w-64 max-w-[calc(100vw-1.25rem)] z-40"
      >
        <Card variant="mixed" class="p-3 space-y-3 shadow-2xl">
          <div class="flex items-center justify-between">
            <div
              class="flex items-center gap-1.5 text-xs font-semibold text-foreground"
            >
              <Icon name="palette" size={14} class="text-accent-foreground" />
              <span>Tema & Cores</span>
            </div>
            <Badge variant="outline" size="xs" class="font-mono">
              {themeMode === "dark" ? "Escuro" : "Claro"}
            </Badge>
          </div>

          <div
            class="grid grid-cols-2 gap-1.5 p-1 rounded-xl bg-muted border border-border"
          >
            <button
              type="button"
              onclick={() => setTheme("dark", themePalette)}
              class="py-1.5 px-2 rounded-lg text-xs font-semibold flex items-center justify-center gap-1.5 transition-all {themeMode ===
              'dark'
                ? 'bg-card text-accent-foreground shadow-xs border border-border'
                : 'text-muted-foreground hover:text-foreground'}"
            >
              <Icon name="moon" size={13} />
              <span>Escuro</span>
            </button>
            <button
              type="button"
              onclick={() => setTheme("light", themePalette)}
              class="py-1.5 px-2 rounded-lg text-xs font-semibold flex items-center justify-center gap-1.5 transition-all {themeMode ===
              'light'
                ? 'bg-card text-accent-foreground shadow-xs border border-border'
                : 'text-muted-foreground hover:text-foreground'}"
            >
              <Icon name="sun" size={13} />
              <span>Claro</span>
            </button>
          </div>

          <div class="space-y-1.5">
            <div class="text-[11px] font-medium text-muted-foreground">
              Paleta de destaque
            </div>
            <div class="grid grid-cols-3 gap-1.5">
              {#each THEME_PALETTES as p}
                <button
                  type="button"
                  onclick={() => setTheme(themeMode, p.id)}
                  class="px-2 py-1.5 rounded-xl border text-[11px] font-medium flex items-center gap-1.5 transition-all active:scale-95 {themePalette ===
                  p.id
                    ? 'bg-accent border-[var(--accent-border)] text-foreground'
                    : 'bg-muted border-border text-muted-foreground hover:border-input'}"
                >
                  <span
                    class="w-3 h-3 rounded-full shrink-0 flex items-center justify-center"
                    style="background-color: {p.swatch};"
                  >
                    {#if themePalette === p.id}
                      <span class="w-1.5 h-1.5 rounded-full bg-white"></span>
                    {/if}
                  </span>
                  <span class="truncate">{p.label}</span>
                </button>
              {/each}
            </div>
          </div>
        </Card>
      </div>
    {/if}
  </header>

  <!-- Tabs & Split Bar (visible when multiple tabs or Split View active) -->
  {#if tabs.length > 1 || splitOpen}
    <div
      class="bg-background/80 backdrop-blur-md border-b border-border px-2.5 sm:px-5 py-1.5 flex items-center justify-between gap-2 shrink-0 select-none theme-surface"
    >
      <div class="flex items-center gap-1.5 overflow-x-auto min-w-0 flex-1">
        {#each tabs as tab, idx (tab.id)}
          {@const isSelected = tab.id === focusedTabId}
          {@const isLeft = splitOpen && tab.id === leftTabId}
          {@const isRight = splitOpen && tab.id === rightTabId}
          <div
            class="inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-[11px] font-medium shrink-0 transition-all {isSelected
              ? 'bg-accent border-[var(--accent-border)] text-accent-foreground shadow-xs'
              : 'bg-muted border-border text-muted-foreground hover:border-input hover:text-foreground'}"
          >
            <button
              type="button"
              onclick={() => selectTab(tab.id)}
              class="flex items-center gap-1.5 max-w-[140px]"
            >
              {#if tab.isGenerating}
                <AiSpinner size={11} class="text-accent-foreground" />
              {/if}
              {#if isLeft}
                <span
                  class="hidden md:inline text-[9px] font-mono uppercase opacity-75"
                  >[Esq]</span
                >
              {:else if isRight}
                <span
                  class="hidden md:inline text-[9px] font-mono uppercase opacity-75"
                  >[Dir]</span
                >
              {/if}
              <span class="truncate">{tab.title || `Sessão ${idx + 1}`}</span>
            </button>
            {#if tabs.length > 1}
              <button
                type="button"
                onclick={(e) => closeTab(e, tab.id)}
                class="text-muted-foreground hover:text-rose-400 transition-colors"
                title="Fechar aba"
              >
                <Icon name="close" size={11} />
              </button>
            {/if}
          </div>
        {/each}

        {#if tabs.length < 6}
          <Button
            variant="outline"
            size="xs"
            onclick={() => createTab(true)}
            title="Abrir nova aba"
          >
            <Icon name="plus" size={12} />
            <span class="hidden sm:inline">Nova Aba</span>
          </Button>
        {/if}
      </div>

      {#if splitOpen}
        <div class="hidden md:flex items-center gap-1.5 shrink-0">
          <Button
            variant={compareOpen ? "default" : "outline"}
            size="xs"
            onclick={() => (compareOpen = !compareOpen)}
            title="Enviar o mesmo prompt para os 2 modelos simultaneamente"
          >
            <Icon name="compare" size={12} />
            <span>Comparar Modelos</span>
          </Button>
        </div>
      {/if}
    </div>
  {/if}

  <!-- Shared Compare Prompt Bar (Desktop Split View) -->
  {#if splitOpen && compareOpen}
    <div
      transition:fly={{ y: -8, duration: 150, easing: cubicOut }}
      class="hidden md:block bg-background/90 backdrop-blur-md border-b border-border px-4 py-2 shrink-0 theme-surface"
    >
      <form
        onsubmit={handleCompareSubmit}
        class="max-w-3xl mx-auto flex items-center gap-2"
      >
        <Icon
          name="compare"
          size={15}
          class="text-accent-foreground shrink-0"
        />
        <Input
          type="text"
          bind:value={comparePrompt}
          placeholder="Comparar modelos: digite um prompt para enviar aos 2 painéis simultaneamente..."
          class="flex-1 rounded-full px-3.5"
        />
        <Button
          type="submit"
          size="sm"
          disabled={!comparePrompt.trim()}
        >
          <Icon name="send" size={13} />
          <span>Enviar aos 2</span>
        </Button>
      </form>
    </div>
  {/if}

  <!-- Workspace (Single View or Desktop Split View) -->
  <div
    class={splitOpen
      ? "flex-1 min-h-0 flex flex-col md:grid md:grid-cols-2 md:gap-3 md:px-3 md:py-2.5"
      : "flex-1 min-h-0 flex flex-col"}
  >
    {#each tabs as tab (tab.id)}
      <ChatPane
        bind:this={paneRefs[tab.id]}
        tabId={tab.id}
        storageKeyPrefix={tab.storageKeyPrefix}
        visibilityMode={visibilityForTab(tab.id)}
        paneOrderClass={orderForTab(tab.id)}
        isSplitView={splitOpen}
        isFocused={tab.id === focusedTabId}
        {models}
        {gatewayMode}
        {hostLabel}
        {features}
        {defaultModel}
        {defaultPermission}
        {slashCommands}
        {outboxFiles}
        {outboxLoaded}
        onOpenShare={openShareModal}
        onRequestDelete={requestDeleteConversation}
        onOpenTerminal={() => (terminalOpen = true)}
        onOpenSlashManager={() => (slashModalOpen = true)}
        onOutboxRefresh={refreshStatusAndOutbox}
        onOpenMediaPreview={(target) => (mediaPreviewTarget = target)}
        onFocus={() => {
          if (splitOpen) {
            focusedSide = tab.id === rightTabId ? "right" : "left";
          }
        }}
        onMetaChange={handlePaneMetaChange}
        onGeneratingChange={handlePaneGeneratingChange}
      />
    {/each}
  </div>
</div>

<Sidebar
  bind:open={sidebarOpen}
  activeConvId={focusedConvId}
  {models}
  watchdogEnabled={features.watchdog !== false}
  telemetryEnabled={features.telemetry === true}
  cockpitEnabled={features.cockpit === true}
  {unreadAlerts}
  {themeMode}
  {themePalette}
  bind:settings
  onSelectConversation={(id, title) => {
    paneRefs[focusedTabId]?.loadConversation(id, title);
  }}
  onNewConversation={() => {
    paneRefs[focusedTabId]?.newConversation();
  }}
  onConversationDeleted={handleConversationDeleted}
  onOpenAutomations={() => (automationsOpen = true)}
  onOpenSlashCommands={() => (slashModalOpen = true)}
  slashCommandCount={slashCommands.length}
  onThemeChange={setTheme}
  onSettingsSaved={(s) => {
    defaultPermission = s.default_permission_mode;
    defaultModel = s.default_model;
  }}
/>

{#if TerminalModalComp}
  <TerminalModalComp
    bind:open={terminalOpen}
    hostLabel={gatewayMode === "standalone"
      ? "Terminal Local"
      : "ryanslx@CachyCore"}
  />
{/if}
{#if OutboxModalComp}
  <OutboxModalComp bind:open={outboxOpen} bind:files={outboxFiles} {outboxDir} />
{/if}
{#if OutboxPreviewModalComp}
  <OutboxPreviewModalComp bind:target={mediaPreviewTarget} />
{/if}
{#if SlashCommandsModalComp}
  <SlashCommandsModalComp
    bind:open={slashModalOpen}
    bind:commands={slashCommands}
    {models}
  />
{/if}
{#if ShareModalComp}
  <ShareModalComp
    bind:open={shareOpen}
    title={shareTitle}
    singleMarkdown={shareSingleMD}
    messages={shareMessages}
    {themeMode}
    {themePalette}
  />
{/if}
{#if AutomationsModalComp}
  <AutomationsModalComp
    bind:open={automationsOpen}
    {models}
    {defaultModel}
    canInstallPWA={Boolean(deferredInstallPrompt)}
    onInstallPWA={handleInstallPWA}
    onOpenConversation={(convId, title) => {
      paneRefs[focusedTabId]?.loadConversation(convId, title);
    }}
    onUnreadChange={(n) => {
      unreadAlerts = n;
    }}
  />
{/if}

{#if pendingDelete}
  <div
    transition:fade={{ duration: 150 }}
    class="fixed inset-0 bg-[var(--bg-backdrop)] backdrop-blur-sm z-50 flex items-end sm:items-center justify-center p-0 sm:p-4"
  >
    <button
      type="button"
      aria-label="Cancelar exclusão"
      onclick={() => {
        if (!deletingChat) pendingDelete = null;
      }}
      class="absolute inset-0 cursor-default"
    ></button>

    <div
      transition:fly={{ y: 20, duration: 200, easing: cubicOut }}
      class="relative w-full sm:max-w-sm"
    >
      <Card
        class="w-full rounded-t-2xl sm:rounded-2xl p-4 space-y-3 shadow-2xl"
      >
        <div class="flex items-start gap-3">
          <div
            class="p-2 rounded-xl bg-rose-500/12 border border-rose-500/25 text-rose-400 shrink-0"
          >
            <Icon name="trash" size={18} />
          </div>
          <div class="min-w-0 flex-1 space-y-1">
            <h2 class="text-sm font-bold text-foreground">
              Apagar chat?
            </h2>
            <p
              class="text-xs text-muted-foreground leading-relaxed break-words"
            >
              A sessão <span class="font-semibold text-foreground"
                >"{pendingDelete.title}"</span
              > e seu histórico serão removidos permanentemente.
            </p>
          </div>
        </div>

        {#if deleteChatError}
          <div
            class="p-2 rounded-lg bg-rose-500/10 border border-rose-500/30 text-xs text-rose-400"
          >
            {deleteChatError}
          </div>
        {/if}

        <div class="flex items-center justify-end gap-2 pt-1">
          <Button
            variant="outline"
            size="sm"
            disabled={deletingChat}
            onclick={() => (pendingDelete = null)}
          >
            Cancelar
          </Button>
          <Button
            variant="destructiveSolid"
            size="sm"
            disabled={deletingChat}
            onclick={confirmDeleteChat}
          >
            <Icon name="trash" size={13} />
            <span>{deletingChat ? "Apagando..." : "Apagar chat"}</span>
          </Button>
        </div>
      </Card>
    </div>
  </div>
{/if}

{#if liveAlertToast}
  <div
    transition:fly={{ y: -16, duration: 180, easing: cubicOut }}
    class="fixed top-16 right-3 z-50 w-80 max-w-[calc(100vw-1.5rem)]"
  >
    <Card class="p-3 shadow-2xl border-[var(--accent-border)] space-y-1.5">
      <div class="flex items-start justify-between gap-2">
        <div class="flex items-center gap-1.5 min-w-0">
          <Icon name="bell" size={14} class="text-accent-foreground shrink-0" />
          <span class="text-xs font-bold text-foreground truncate"
            >{liveAlertToast.title}</span
          >
        </div>
        <button
          type="button"
          onclick={() => (liveAlertToast = null)}
          class="text-muted-foreground hover:text-foreground shrink-0"
          aria-label="Fechar alerta"
        >
          <Icon name="close" size={14} />
        </button>
      </div>
      <p
        class="text-[11px] text-secondary-foreground line-clamp-3 break-words"
      >
        {liveAlertToast.message}
      </p>
      <div class="flex items-center justify-between gap-2 pt-0.5">
        <div>
          {#if typeof window !== "undefined" && window.isSecureContext && "Notification" in window && browserNotifPerm === "default"}
            <Button
              variant="link"
              size="xs"
              onclick={handleEnableBrowserFromToast}
              class="px-0 h-auto text-[11px]"
            >
              Ativar no navegador
            </Button>
          {/if}
        </div>
        {#if liveAlertToast.conv_id}
          <Button
            variant="link"
            size="xs"
            onclick={() => {
              if (liveAlertToast?.conv_id) {
                paneRefs[focusedTabId]?.loadConversation(
                  liveAlertToast.conv_id,
                  liveAlertToast.title,
                );
              }
              liveAlertToast = null;
            }}
            class="px-0 h-auto text-[11px] font-semibold"
          >
            Abrir sessão
          </Button>
        {:else}
          <Button
            variant="link"
            size="xs"
            onclick={() => {
              liveAlertToast = null;
              automationsOpen = true;
            }}
            class="px-0 h-auto text-[11px] font-semibold"
          >
            Ver alertas
          </Button>
        {/if}
      </div>
    </Card>
  </div>
{/if}

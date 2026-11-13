export type PermissionMode = 'readonly' | 'copilot' | 'autonomous';

export interface ModelInfo {
  id: string;
  name: string;
  type: 'cloud' | 'local';
  badge: string;
}

export interface ConversationSummary {
  id: string;
  title: string;
  updated_at: string;
  engine: 'agy' | 'ollama';
  model?: string;
  message_count?: number;
}

export interface ChatMessage {
  role: 'user' | 'agent';
  content: string;
}

export interface GatewaySettings {
  default_permission_mode: PermissionMode;
  default_model: string;
  default_effort: string;
}

export interface OutboxFile {
  name: string;
  size: number;
  mtime: number;
}

export interface WatchdogState {
  online: boolean | null;
  last_checked: string | null;
  last_seen_online: string | null;
  uptime_checks: number;
  downtime_events: number;
}

export async function fetchStatus(): Promise<{ online: boolean; target_host: string; target_user: string }> {
  const res = await fetch('/api/status');
  return res.json();
}

export async function fetchWatchdog(): Promise<WatchdogState> {
  const res = await fetch('/api/watchdog');
  return res.json();
}

export async function fetchFeatures(): Promise<{ version: string; features: Record<string, any> }> {
  const res = await fetch('/api/features');
  return res.json();
}

export async function triggerWol(): Promise<{ ok: boolean; message: string }> {
  const res = await fetch('/api/wol', { method: 'POST' });
  return res.json();
}

export async function fetchModels(): Promise<ModelInfo[]> {
  const res = await fetch('/api/models');
  const data = await res.json();
  return data.models ?? [];
}

export async function fetchSettings(): Promise<GatewaySettings> {
  const res = await fetch('/api/settings');
  return res.json();
}

export async function updateSettings(patch: Partial<GatewaySettings>): Promise<GatewaySettings> {
  const res = await fetch('/api/settings', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  });
  return res.json();
}

export async function fetchConversations(
  q = '',
  limit = 40,
  offset = 0
): Promise<{ conversations: ConversationSummary[]; total: number; has_more: boolean }> {
  const params = new URLSearchParams({ limit: String(limit), offset: String(offset) });
  if (q.trim()) params.set('q', q.trim());
  const res = await fetch(`/api/conversations?${params}`);
  return res.json();
}

export async function fetchConversation(
  id: string
): Promise<{ id: string; messages: ChatMessage[]; active_job_id?: string | null; engine?: string }> {
  const res = await fetch(`/api/conversations/${encodeURIComponent(id)}`);
  if (!res.ok) throw new Error('Conversa não encontrada');
  return res.json();
}

export async function deleteConversation(id: string): Promise<void> {
  await fetch(`/api/conversations/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export async function uploadFile(file: File): Promise<{ ok: boolean; filename: string; path: string; size: number }> {
  const form = new FormData();
  form.append('file', file);
  const res = await fetch('/api/upload', { method: 'POST', body: form });
  if (!res.ok) throw new Error('Falha no upload');
  return res.json();
}

export async function fetchOutbox(): Promise<OutboxFile[]> {
  const res = await fetch('/api/outbox');
  const data = await res.json();
  return data.files ?? [];
}

export async function deleteOutboxFile(name: string): Promise<void> {
  await fetch(`/api/outbox/${encodeURIComponent(name)}`, { method: 'DELETE' });
}

export async function createJob(payload: {
  prompt: string;
  conversation_id: string | null;
  model: string;
  permission_mode: PermissionMode;
  attached_files?: string[];
}): Promise<{ job_id: string; conversation_id: string | null; engine: string; model?: string; status: string }> {
  const res = await fetch('/api/jobs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

export async function cancelJob(jobId: string): Promise<void> {
  await fetch(`/api/jobs/${encodeURIComponent(jobId)}/cancel`, { method: 'POST' });
}

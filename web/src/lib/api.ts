export type Agent = {
  id: string;
  name: string;
  base_url: string;
  version: string;
  read_only: boolean;
  status: 'healthy' | 'degraded' | 'offline';
  last_heartbeat: string;
  last_sync: string;
};
export type Container = {
  id: string;
  agent_id: string;
  docker_id: string;
  name: string;
  image: string;
  tag: string;
  current_digest?: string;
  remote_digest?: string;
  update_available: boolean;
  detection_method?: string;
  runtime_status: string;
  management_kind: 'compose' | 'docker-run';
  compose_project?: string;
  compose_service?: string;
  compose_working_dir?: string;
  labels: Record<string, string>;
  ignored: boolean;
  protected: boolean;
  sensitive: boolean;
  manageable: boolean;
  safety_reason?: string;
  last_sync: string;
};
export type Job = {
  id: string;
  agent_job_id?: string;
  agent_id: string;
  container_id?: string;
  target_key: string;
  action: string;
  status: string;
  requested_by: string;
  correlation_id: string;
  dry_run_plan?: string;
  error?: string;
  created_at: string;
  started_at?: string;
  finished_at?: string;
};
export type JobEvent = { id: number; job_id: string; level: string; message: string; created_at: string };
export type SystemInfo = {
  version: string;
  target_version: string;
  agents: Agent[];
};
export type PreflightCheck = {
  key: string;
  label: string;
  status: 'pass' | 'warn' | 'block';
  message: string;
};
export type UpdatePreflight = {
  container_id: string;
  agent_id: string;
  scope: string;
  can_dry_run: boolean;
  can_update: boolean;
  checks: PreflightCheck[];
};
export type DryRunPlan = {
  target: string;
  scope: string;
  steps: string[];
  warnings?: string[];
};

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(`/api/v1${path}`, {
    ...init,
    headers: { 'Content-Type': 'application/json', 'X-DockPulse-User': 'web-admin', ...(init.headers || {}) }
  });
  const body = res.status === 204 ? null : await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body?.error || `Request failed (${res.status})`);
  return body as T;
}
export const short = (v?: string) => (v ? v.replace('sha256:', '').slice(0, 12) : '—');
export const imageVersion = (c: Container) => c.tag || c.image.split(':').at(-1) || 'latest';
export const jobActionLabel = (action: string) => {
  if (action === 'dry-run') return 'Preview';
  if (action === 'self-update') return 'Self-update';
  if (action === 'update') return 'Update';
  return action;
};
export const jobActionDescription = (action: string) => {
  if (action === 'dry-run') return 'Preview only. Docker was not changed.';
  if (action === 'self-update') return 'DockPulse agent release update.';
  if (action === 'update') return 'Docker workload update.';
  return 'Control-plane job.';
};
export const jobTarget = (j: Job) => j.target_key.split(':').at(-1) || j.target_key;
export const parsePlan = (v?: string): DryRunPlan | null => {
  if (!v) return null;
  try {
    return JSON.parse(v) as DryRunPlan;
  } catch {
    return null;
  }
};
export const since = (v?: string) => {
  if (!v) return 'never';
  const d = (Date.now() - new Date(v).getTime()) / 1000;
  if (d < 60) return `${Math.max(0, Math.round(d))}s ago`;
  if (d < 3600) return `${Math.round(d / 60)}m ago`;
  if (d < 86400) return `${Math.round(d / 3600)}h ago`;
  return `${Math.round(d / 86400)}d ago`;
};

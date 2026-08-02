<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/state';
  import { api, since, type Job, type JobEvent } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let job = $state<Job | null>(null);
  let events = $state<JobEvent[]>([]);
  let error = $state('');
  let source = $state<EventSource | null>(null);
  async function load() {
    try {
      [job, events] = await Promise.all([
        api<Job>(`/jobs/${page.params.id}`),
        api<JobEvent[]>(`/jobs/${page.params.id}/logs`)
      ]);
      if (job && ['queued', 'running'].includes(job.status)) stream();
    } catch (e) {
      error = (e as Error).message;
    }
  }
  function stream() {
    source?.close();
    source = new EventSource(`/api/v1/jobs/${page.params.id}/events`);
    source.addEventListener('log', (event) => {
      const e = JSON.parse((event as MessageEvent).data) as JobEvent;
      if (!events.some((v) => v.id === e.id)) events = [...events, e];
      void refreshJob();
    });
  }
  async function refreshJob() {
    job = await api<Job>(`/jobs/${page.params.id}`);
    if (job && !['queued', 'running'].includes(job.status)) source?.close();
  }
  onMount(load);
  onDestroy(() => source?.close());
</script>

<section class="page">
  {#if job}<header class="page-head">
      <div>
        <span class="eyebrow">Execution detail</span>
        <h1>{job.action} <span class="mono">{job.id}</span></h1>
        <p>{job.target_key} · requested {since(job.created_at)} by {job.requested_by}</p>
      </div>
      <Status value={job.status} />
    </header>
    {#if job.error}<div class="callout errorbox">{job.error}</div>{/if}{#if job.dry_run_plan}<section
        class="panel"
        style="margin-bottom:16px"
      >
        <div class="panel-head"><h2>Dry-run plan</h2></div>
        <div style="padding:16px"><pre>{job.dry_run_plan}</pre></div>
      </section>{/if}
    <section class="panel">
      <div class="panel-head">
        <div>
          <h2>Execution log</h2>
          <span>SSE live stream · correlation {job.correlation_id || 'n/a'}</span>
        </div>
        <span>{events.length} events</span>
      </div>
      <div class="log">
        {#each events as e}<div class="log-row">
            <span>{new Date(e.created_at).toLocaleTimeString()}</span><span class={e.level}>{e.level}</span
            ><span>{e.message}</span>
          </div>{:else}<div>Waiting for agent events…</div>{/each}
      </div>
    </section>{:else if error}<div class="callout errorbox">{error}</div>{/if}
</section>

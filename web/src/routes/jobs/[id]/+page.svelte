<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { page } from '$app/state';
  import {
    api,
    jobActionDescription,
    jobActionLabel,
    parsePlan,
    since,
    type Job,
    type JobEvent
  } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let job = $state<Job | null>(null);
  let events = $state<JobEvent[]>([]);
  let error = $state('');
  let source = $state<EventSource | null>(null);
  const plan = $derived(parsePlan(job?.dry_run_plan));
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
        <span class="eyebrow">{job.action === 'dry-run' ? 'Inspection detail' : 'Execution detail'}</span>
        <h1>{jobActionLabel(job.action)} <span class="mono">{job.id}</span></h1>
        <p>
          {job.target_key} · {jobActionDescription(job.action)} · requested {since(job.created_at)} by {job.requested_by}
        </p>
      </div>
      <Status value={job.status} />
    </header>
    {#if job.error}<div class="callout errorbox">{job.error}</div>{/if}{#if job.dry_run_plan}<section
        class="panel"
        style="margin-bottom:16px"
      >
        <div class="panel-head">
          <h2>Dry-run inspection</h2>
          <span>This is the plan DockPulse would execute after confirmation</span>
        </div>
        <div style="padding:16px">
          <div class="callout">
            No Docker changes were made. This result is saved for audit so you can review it before running
            Update now.
          </div>
          {#if plan}
            <div class="plan">
              <div class="plan-summary">
                <div><span>Target</span><strong>{plan.target}</strong></div>
                <div><span>Scope</span><strong>{plan.scope}</strong></div>
              </div>
              <ol class="plan-steps">
                {#each plan.steps as step}<li>{step}</li>{/each}
              </ol>
              {#if plan.warnings?.length}
                <div class="plan-warnings">
                  {#each plan.warnings as warning}<div class="callout">{warning}</div>{/each}
                </div>
              {/if}
            </div>
          {:else}
            <pre>{job.dry_run_plan}</pre>
          {/if}
        </div>
      </section>{/if}
    <section class="panel">
      <div class="panel-head">
        <div>
          <h2>Execution log</h2>
          <span
            >{job.action === 'dry-run'
              ? 'Validation result'
              : `SSE live stream · correlation ${job.correlation_id || 'n/a'}`}</span
          >
        </div>
        <span>{events.length} events</span>
      </div>
      <div class="log">
        {#each events as e}<div class="log-row">
            <span>{new Date(e.created_at).toLocaleTimeString()}</span><span class={e.level}>{e.level}</span
            ><span>{e.message}</span>
          </div>{:else}<div>
            {job.action === 'dry-run'
              ? 'Dry-run completed. Live execution logs only appear when you run Update now.'
              : 'Waiting for agent events...'}
          </div>{/each}
      </div>
    </section>{:else if error}<div class="callout errorbox">{error}</div>{/if}
</section>

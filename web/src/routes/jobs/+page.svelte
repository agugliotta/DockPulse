<script lang="ts">
  import { onMount } from 'svelte';
  import { api, jobActionDescription, jobActionLabel, since, type Job } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let jobs = $state<Job[]>([]);
  let loading = $state(true);
  let error = $state('');
  onMount(async () => {
    try {
      jobs = await api<Job[]>('/jobs?limit=200');
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  });
</script>

{#if loading}<div class="loading"></div>{/if}
<section class="page">
  <header class="page-head">
    <div>
      <span class="eyebrow">Audit trail</span>
      <h1>Activity</h1>
      <p>Inspections, update executions, actors and results in one timeline.</p>
    </div>
  </header>
  {#if error}<div class="callout errorbox">{error}</div>{/if}
  <section class="panel">
    <div class="panel-head">
      <h2>Jobs</h2>
      <span>{jobs.length} most recent</span>
    </div>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Job</th><th>Type</th><th>Status</th><th>Requested by</th><th>Created</th></tr></thead
        ><tbody
          >{#each jobs as j}<tr
              ><td
                ><a class="name mono" href={`/jobs/${j.id}`}>{j.id}</a><span class="sub">{j.target_key}</span
                ></td
              ><td>{jobActionLabel(j.action)}<span class="sub">{jobActionDescription(j.action)}</span></td><td
                ><Status value={j.status} /></td
              ><td>{j.requested_by}</td><td>{since(j.created_at)}</td></tr
            >{:else}<tr><td colspan="5" class="empty">No operations recorded yet.</td></tr>{/each}</tbody
        >
      </table>
    </div>
  </section>
</section>

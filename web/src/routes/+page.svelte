<script lang="ts">
  import { onMount } from 'svelte';
  import { api, since, type Agent, type Container, type Job } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let agents = $state<Agent[]>([]);
  let containers = $state<Container[]>([]);
  let jobs = $state<Job[]>([]);
  let loading = $state(true);
  let error = $state('');
  async function load() {
    loading = true;
    error = '';
    try {
      [agents, containers, jobs] = await Promise.all([
        api<Agent[]>('/agents'),
        api<Container[]>('/containers'),
        api<Job[]>('/jobs?limit=8')
      ]);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
  onMount(load);
  const updates = $derived(containers.filter((c) => c.update_available && !c.ignored));
  const online = $derived(agents.filter((a) => a.status === 'healthy').length);
  const active = $derived(jobs.filter((j) => ['queued', 'running'].includes(j.status)).length);
</script>

{#if loading}<div class="loading"></div>{/if}
<section class="page">
  <header class="page-head">
    <div>
      <span class="eyebrow">Fleet operations</span>
      <h1>System overview</h1>
      <p>Image freshness and update posture across every Docker host.</p>
    </div>
    <div class="actions"><button class="btn" onclick={load}>↻ Refresh</button></div>
  </header>
  {#if error}<div class="callout errorbox">{error}</div>{/if}
  <div class="grid stats">
    <div class="stat">
      <span class="label">Agents online</span><strong>{online}<small> / {agents.length}</small></strong><small
        >{agents.length - online} require attention</small
      >
    </div>
    <div class="stat">
      <span class="label">Workloads</span><strong>{containers.length}</strong><small
        >Across {agents.length} registered agents</small
      >
    </div>
    <div class="stat attn">
      <span class="label">Updates ready</span><strong>{updates.length}</strong><small
        >{containers.filter((c) => c.ignored).length} excluded by policy</small
      >
    </div>
    <div class="stat">
      <span class="label">Active jobs</span><strong>{active}</strong><small
        >Persistent target locking enabled</small
      >
    </div>
  </div>
  <div class="grid split">
    <section class="panel">
      <div class="panel-head">
        <div>
          <h2>Update candidates</h2>
          <span>Digest changes verified against registry metadata</span>
        </div>
        <a class="btn" href="/containers">View inventory</a>
      </div>
      <div class="table-wrap">
        <table>
          <thead><tr><th>Workload</th><th>Source</th><th>Digest</th><th>Policy</th></tr></thead><tbody
            >{#each updates.slice(0, 8) as c}<tr
                ><td
                  ><a class="name" href={`/containers/${encodeURIComponent(c.id)}`}>{c.name}</a><span
                    class="sub">{c.image}</span
                  ></td
                ><td
                  ><span class="mono"
                    >{c.management_kind}{c.compose_project ? ` · ${c.compose_project}` : ''}</span
                  ></td
                ><td
                  ><Status value="update-available" label="Update" /><span class="sub"
                    >{c.detection_method}</span
                  ></td
                ><td
                  >{#if c.sensitive}<Status
                      value="protected"
                      label="Sensitive"
                    />{:else if !c.manageable}<Status value="ignored" label="Review" />{:else}<Status
                      value="healthy"
                      label="Eligible"
                    />{/if}</td
                ></tr
              >{:else}<tr><td colspan="4" class="empty">No actionable image updates detected.</td></tr
              >{/each}</tbody
          >
        </table>
      </div>
    </section>
    <section class="panel">
      <div class="panel-head">
        <div>
          <h2>Recent activity</h2>
          <span>Latest control-plane jobs</span>
        </div>
        <a href="/jobs" class="btn">All jobs</a>
      </div>
      <ul class="activity">
        {#each jobs as j}<li>
            <i class:success={j.status === 'succeeded'} class:failure={j.status === 'failed'}></i>
            <div>
              <a href={`/jobs/${j.id}`}><strong>{j.action} · {j.target_key.split(':').at(-1)}</strong></a
              ><small>{j.status} · {since(j.created_at)} · {j.requested_by}</small>
            </div>
          </li>{:else}<li><div>No job history yet.</div></li>{/each}
      </ul>
    </section>
  </div>
</section>

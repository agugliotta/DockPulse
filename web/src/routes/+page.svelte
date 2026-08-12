<script lang="ts">
  import { onMount } from 'svelte';
  import {
    api,
    jobActionDescription,
    jobActionLabel,
    jobTarget,
    since,
    type Agent,
    type Container,
    type Job
  } from '$lib/api';
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
        api<Job[]>('/jobs?limit=20')
      ]);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
  onMount(load);
  const orderedJobs = $derived(
    [...jobs].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())
  );
  const updates = $derived(containers.filter((c) => c.update_available && !c.ignored));
  const healthyAgents = $derived(agents.filter((a) => a.status === 'healthy'));
  const degradedAgents = $derived(agents.filter((a) => a.status === 'degraded'));
  const offlineAgents = $derived(agents.filter((a) => a.status === 'offline'));
  const readOnlyAgents = $derived(agents.filter((a) => a.read_only));
  const updateJobs = $derived(orderedJobs.filter((j) => j.action === 'update'));
  const previewJobs = $derived(orderedJobs.filter((j) => j.action === 'dry-run'));
  const selfUpdateJobs = $derived(orderedJobs.filter((j) => j.action === 'self-update'));
  const failedJobs = $derived(orderedJobs.filter((j) => j.status === 'failed'));
  const online = $derived(agents.filter((a) => a.status === 'healthy').length);
  const active = $derived(jobs.filter((j) => ['queued', 'running'].includes(j.status)).length);
  const activityFeed = $derived(orderedJobs.slice(0, 8));
  const share = (value: number, total: number) =>
    total ? Math.max(6, Math.round((value / total) * 100)) : 0;
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
  <div class="grid analytics-grid">
    <section class="panel analytics-panel">
      <div class="panel-head">
        <div>
          <h2>Fleet signals</h2>
          <span>Simple health and change mix for the last sync window</span>
        </div>
      </div>
      <div class="analytics-cards">
        <article class="analytics-card">
          <div class="analytics-card-head">
            <div>
              <h3>Agent health</h3>
              <span>Where the fleet is stable or needs review</span>
            </div>
            <strong>{agents.length}</strong>
          </div>
          <div class="metric-list">
            <div class="metric-row">
              <div><span>Healthy</span><strong>{healthyAgents.length}</strong></div>
              <div class="metric-track">
                <div
                  class="metric-fill healthy"
                  style={`width:${share(healthyAgents.length, agents.length)}%`}
                ></div>
              </div>
            </div>
            <div class="metric-row">
              <div><span>Degraded</span><strong>{degradedAgents.length}</strong></div>
              <div class="metric-track">
                <div
                  class="metric-fill warn"
                  style={`width:${share(degradedAgents.length, agents.length)}%`}
                ></div>
              </div>
            </div>
            <div class="metric-row">
              <div><span>Offline</span><strong>{offlineAgents.length}</strong></div>
              <div class="metric-track">
                <div
                  class="metric-fill danger"
                  style={`width:${share(offlineAgents.length, agents.length)}%`}
                ></div>
              </div>
            </div>
            <div class="metric-row">
              <div><span>Read only</span><strong>{readOnlyAgents.length}</strong></div>
              <div class="metric-track">
                <div
                  class="metric-fill neutral"
                  style={`width:${share(readOnlyAgents.length, agents.length)}%`}
                ></div>
              </div>
            </div>
          </div>
        </article>
        <article class="analytics-card">
          <div class="analytics-card-head">
            <div>
              <h3>Recent change mix</h3>
              <span>What this overview has seen recently</span>
            </div>
            <strong>{orderedJobs.length}</strong>
          </div>
          <div class="metric-list">
            <div class="metric-row">
              <div><span>Updates</span><strong>{updateJobs.length}</strong></div>
              <div class="metric-track">
                <div
                  class="metric-fill"
                  style={`width:${share(updateJobs.length, orderedJobs.length)}%`}
                ></div>
              </div>
            </div>
            <div class="metric-row">
              <div><span>Previews</span><strong>{previewJobs.length}</strong></div>
              <div class="metric-track">
                <div
                  class="metric-fill muted"
                  style={`width:${share(previewJobs.length, orderedJobs.length)}%`}
                ></div>
              </div>
            </div>
            <div class="metric-row">
              <div><span>Self updates</span><strong>{selfUpdateJobs.length}</strong></div>
              <div class="metric-track">
                <div
                  class="metric-fill blue"
                  style={`width:${share(selfUpdateJobs.length, orderedJobs.length)}%`}
                ></div>
              </div>
            </div>
            <div class="metric-row">
              <div><span>Failures</span><strong>{failedJobs.length}</strong></div>
              <div class="metric-track">
                <div
                  class="metric-fill danger"
                  style={`width:${share(failedJobs.length, orderedJobs.length)}%`}
                ></div>
              </div>
            </div>
          </div>
        </article>
      </div>
    </section>
  </div>
  <section class="panel">
    <div class="panel-head">
      <div>
        <h2>Workload update candidates</h2>
        <span>Services with registry digest changes</span>
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
  <section class="panel" style="margin-top:16px">
    <div class="panel-head">
      <div>
        <h2>Recent activity</h2>
        <span>Changes and previews in one chronological feed</span>
      </div>
      <a href="/jobs" class="btn">All activity</a>
    </div>
    <ul class="activity compact activity-feed">
      {#each activityFeed as j}<li>
          <i
            class:success={j.status === 'succeeded'}
            class:failure={j.status === 'failed'}
            class:inspection={j.action === 'dry-run'}
          ></i>
          <div>
            <div class="activity-topline">
              <span class={`activity-chip ${j.action}`}>{jobActionLabel(j.action)}</span>
              <span class="activity-meta">{j.status}</span>
              <span class="activity-meta">{since(j.created_at)}</span>
            </div>
            <a href={`/jobs/${j.id}`}><strong>{jobTarget(j)}</strong></a>
            <small>{jobActionDescription(j.action)} · requested by {j.requested_by}</small>
          </div>
        </li>{:else}<li><div class="empty">No activity yet.</div></li>{/each}
    </ul>
  </section>
</section>

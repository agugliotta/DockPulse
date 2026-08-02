<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { api, since, type Agent, type Container } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let agent = $state<Agent | null>(null);
  let containers = $state<Container[]>([]);
  let loading = $state(true);
  let error = $state('');
  async function load() {
    loading = true;
    try {
      [agent, containers] = await Promise.all([
        api<Agent>(`/agents/${page.params.id}`),
        api<Container[]>(`/agents/${page.params.id}/containers`)
      ]);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
  async function refresh() {
    loading = true;
    try {
      await api(`/agents/${page.params.id}/refresh`, { method: 'POST', body: '{}' });
      await load();
    } catch (e) {
      error = (e as Error).message;
      loading = false;
    }
  }
  onMount(load);
</script>

{#if loading}<div class="loading"></div>{/if}
<section class="page">
  {#if agent}<header class="page-head">
      <div>
        <span class="eyebrow">Agent detail</span>
        <h1>{agent.name}</h1>
        <p>{agent.id} · {agent.base_url}</p>
      </div>
      <div class="actions">
        <Status value={agent.status} /><button
          class="btn primary"
          disabled={agent.status === 'offline'}
          onclick={refresh}>↻ Refresh inventory</button
        >
      </div>
    </header>
    <div class="grid stats">
      <div class="stat">
        <span class="label">Workloads</span><strong>{containers.length}</strong><small
          >{containers.filter((c) => c.runtime_status === 'running').length} currently running</small
        >
      </div>
      <div class="stat attn">
        <span class="label">Updates</span><strong
          >{containers.filter((c) => c.update_available).length}</strong
        ><small>Registry digest changes</small>
      </div>
      <div class="stat">
        <span class="label">Heartbeat</span><strong style="font-size:20px"
          >{since(agent.last_heartbeat)}</strong
        ><small>{agent.status}</small>
      </div>
      <div class="stat">
        <span class="label">Execution</span><strong style="font-size:20px"
          >{agent.read_only ? 'Read only' : 'Enabled'}</strong
        ><small>Agent-side enforcement</small>
      </div>
    </div>
    <section class="panel">
      <div class="panel-head">
        <h2>Local inventory</h2>
        <span>synced {since(agent.last_sync)}</span>
      </div>
      <div class="table-wrap">
        <table>
          <thead
            ><tr><th>Container</th><th>Runtime</th><th>Orchestrator</th><th>Freshness</th><th>Policy</th></tr
            ></thead
          ><tbody
            >{#each containers as c}<tr
                ><td
                  ><a class="name" href={`/containers/${encodeURIComponent(c.id)}`}>{c.name}</a><span
                    class="sub">{c.image}</span
                  ></td
                ><td><Status value={c.runtime_status} /></td><td
                  >{c.management_kind}<span class="sub">{c.compose_project || 'standalone'}</span></td
                ><td
                  >{#if c.update_available}<Status value="update-available" label="Update" />{:else}<Status
                      value="healthy"
                      label="Current"
                    />{/if}</td
                ><td
                  >{#if c.ignored}<Status value="ignored" />{:else if c.protected || c.sensitive}<Status
                      value="protected"
                    />{:else if c.manageable}<Status value="healthy" label="Managed" />{:else}<Status
                      value="degraded"
                      label="Review"
                    />{/if}</td
                ></tr
              >{/each}</tbody
          >
        </table>
      </div>
    </section>{:else if error}<div class="callout errorbox">{error}</div>{/if}
</section>

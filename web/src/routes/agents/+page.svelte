<script lang="ts">
  import { onMount } from 'svelte';
  import { api, since, type Agent, type Container } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let agents = $state<Agent[]>([]);
  let containers = $state<Container[]>([]);
  let loading = $state(true);
  let error = $state('');
  async function load() {
    loading = true;
    try {
      [agents, containers] = await Promise.all([api<Agent[]>('/agents'), api<Container[]>('/containers')]);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
  onMount(load);
  const count = (id: string) => containers.filter((c) => c.agent_id === id).length;
  const readyAgents = $derived(agents.filter((a) => a.status === 'healthy' && !a.read_only));
  const attentionAgents = $derived(agents.filter((a) => a.status !== 'healthy' || a.read_only));
  const offlineAgents = $derived(agents.filter((a) => a.status === 'offline'));
  const writableAgents = $derived(agents.filter((a) => !a.read_only));
</script>

{#if loading}<div class="loading"></div>{/if}
<section class="page">
  <header class="page-head">
    <div>
      <span class="eyebrow">Fleet</span>
      <h1>Agents</h1>
      <p>One isolated execution boundary per Docker host or LXC.</p>
    </div>
    <button class="btn" onclick={load}>↻ Refresh</button>
  </header>
  {#if error}<div class="callout errorbox">{error}</div>{/if}
  <section class="panel" style="margin-bottom:16px">
    <div class="panel-head">
      <div>
        <h2>Agent readiness</h2>
        <span>Agents are reviewed here, not in the overview</span>
      </div>
      <span>{readyAgents.length} ready</span>
    </div>
    <div class="readiness-board">
      <div class="readiness-summary">
        <div class:ready={readyAgents.length === agents.length && agents.length > 0}>
          <strong>{readyAgents.length}</strong>
          <span>Ready</span>
          <small>Healthy and writable</small>
        </div>
        <div class:ready={attentionAgents.length === 0}>
          <strong>{attentionAgents.length}</strong>
          <span>Attention</span>
          <small>{attentionAgents.length === 0 ? 'No blockers' : 'Need review'}</small>
        </div>
        <div class:ready={offlineAgents.length === 0}>
          <strong>{offlineAgents.length}</strong>
          <span>Offline</span>
          <small>{offlineAgents.length === 0 ? 'All online' : 'Heartbeat missing'}</small>
        </div>
      </div>
      {#if attentionAgents.length}
        <ul class="activity compact">
          {#each attentionAgents.slice(0, 6) as agent}
            <li>
              <i
                class:success={agent.status === 'healthy' && !agent.read_only}
                class:failure={agent.status === 'offline'}
              ></i>
              <div>
                <a href={`/agents/${agent.id}`}><strong>{agent.name}</strong></a><small
                  >{agent.status} · {agent.read_only ? 'read-only' : 'writable'} · heartbeat {since(
                    agent.last_heartbeat
                  )}</small
                >
              </div>
            </li>
          {/each}
        </ul>
      {:else}
        <div class="callout">All agents are healthy and writable.</div>
      {/if}
      <div class="sub" style="padding:0 2px">
        {writableAgents.length} writable · {agents.length - writableAgents.length} read only
      </div>
    </div>
  </section>
  <section class="panel">
    <div class="panel-head">
      <h2>Registered agents</h2>
      <span>{agents.length} total</span>
    </div>
    <div class="table-wrap">
      <table>
        <thead
          ><tr><th>Host</th><th>Health</th><th>Inventory</th><th>Last heartbeat</th><th>Mode</th></tr></thead
        ><tbody
          >{#each agents as a}<tr
              ><td
                ><a class="name" href={`/agents/${a.id}`}>{a.name}</a><span class="sub"
                  >{a.id} · {a.version}</span
                ></td
              ><td><Status value={a.status} /></td><td
                >{count(a.id)} workloads<span class="sub">synced {since(a.last_sync)}</span></td
              ><td>{since(a.last_heartbeat)}</td><td
                >{#if a.read_only}<Status value="protected" label="Read only" />{:else}<Status
                    value="healthy"
                    label="Updates enabled"
                  />{/if}</td
              ></tr
            >{:else}<tr
              ><td colspan="5" class="empty"
                >No agents registered. Start an agent with the shared bootstrap token.</td
              ></tr
            >{/each}</tbody
        >
      </table>
    </div>
  </section>
</section>

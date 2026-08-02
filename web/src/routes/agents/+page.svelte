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

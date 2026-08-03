<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, since, type Job, type SystemInfo } from '$lib/api';
  import Status from '$lib/Status.svelte';

  let system = $state<SystemInfo | null>(null);
  let loading = $state(true);
  let error = $state('');
  let busyAgent = $state('');

  async function load() {
    loading = true;
    error = '';
    try {
      system = await api<SystemInfo>('/system');
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }

  async function selfUpdate(agentID: string) {
    busyAgent = agentID;
    error = '';
    try {
      const job = await api<Job>(`/agents/${agentID}/self-update`, {
        method: 'POST',
        body: JSON.stringify({ confirm: true, target_version: system?.target_version })
      });
      await goto(`/jobs/${job.id}`);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      busyAgent = '';
    }
  }

  onMount(load);

  const releasePath = $derived(system ? `${system.version || 'unknown'} -> ${system.target_version}` : '');
</script>

{#if loading}<div class="loading"></div>{/if}
<section class="page">
  <header class="page-head">
    <div>
      <span class="eyebrow">Release management</span>
      <h1>System</h1>
      <p>Current control version, configured target and opt-in agent self-updates.</p>
    </div>
    <button class="btn" onclick={load}>Refresh</button>
  </header>
  {#if error}<div class="callout errorbox">{error}</div>{/if}
  {#if system}
    <div class="grid stats">
      <div class="stat">
        <span class="label">Control path</span><strong>{system.version}</strong><small
          >to {system.target_version}</small
        >
      </div>
      <div class="stat attn">
        <span class="label">Release path</span><strong>{releasePath}</strong><small>Current to target</small>
      </div>
      <div class="stat">
        <span class="label">Agents</span><strong>{system.agents.length}</strong><small
          >{system.agents.filter((a) => !a.read_only).length} update-enabled</small
        >
      </div>
      <div class="stat">
        <span class="label">Online</span><strong
          >{system.agents.filter((a) => a.status !== 'offline').length}</strong
        ><small>Heartbeat observed</small>
      </div>
    </div>
    <div class="callout">
      If the deployment points at <span class="mono">latest</span>, DockPulse shows that target tag while
      Docker resolves the exact image digest during <span class="mono">docker compose pull</span>.
    </div>
    <section class="panel">
      <div class="panel-head">
        <div>
          <h2>Agent self-updates</h2>
          <span>Requires opt-in self-update variables and a writable agent mode</span>
        </div>
      </div>
      <div class="table-wrap">
        <table>
          <thead
            ><tr><th>Agent</th><th>Version path</th><th>Health</th><th>Mode</th><th>Action</th></tr></thead
          >
          <tbody>
            {#each system.agents as agent}
              <tr>
                <td
                  ><a class="name" href={`/agents/${agent.id}`}>{agent.name}</a><span class="sub"
                    >{agent.id}</span
                  ></td
                >
                <td>
                  <div class="version-flow">
                    <div class="path">
                      <span class="version-token">{agent.version || 'unknown'}</span><span class="arrow"
                        >-&gt;</span
                      ><span class="version-token target">{system.target_version}</span>
                    </div>
                    <span class="sub">agent release target</span>
                  </div>
                </td>
                <td><Status value={agent.status} /><span class="sub">{since(agent.last_heartbeat)}</span></td>
                <td>
                  {#if agent.read_only}<Status value="protected" label="Read only" />{:else}<Status
                      value="healthy"
                      label="Writable"
                    />{/if}
                </td>
                <td>
                  <button
                    class="btn primary"
                    disabled={agent.status === 'offline' || agent.read_only || busyAgent === agent.id}
                    onclick={() => selfUpdate(agent.id)}
                    >{busyAgent === agent.id ? 'Starting...' : 'Self-update'}</button
                  >
                </td>
              </tr>
            {:else}
              <tr><td colspan="5" class="empty">No agents registered yet.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
</section>

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
        <span class="label">Control version</span><strong>{system.version}</strong><small
          >Running binary</small
        >
      </div>
      <div class="stat attn">
        <span class="label">Update target</span><strong>{system.target_version}</strong><small
          >Compose tag target</small
        >
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
          <thead><tr><th>Agent</th><th>Current</th><th>Target</th><th>Health</th><th>Action</th></tr></thead>
          <tbody>
            {#each system.agents as agent}
              <tr>
                <td
                  ><a class="name" href={`/agents/${agent.id}`}>{agent.name}</a><span class="sub"
                    >{agent.id}</span
                  ></td
                >
                <td class="mono">{agent.version || 'unknown'}</td>
                <td class="mono">{system.target_version}</td>
                <td><Status value={agent.status} /><span class="sub">{since(agent.last_heartbeat)}</span></td>
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

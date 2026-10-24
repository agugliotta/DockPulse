<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { api, since, type Job, type SystemInfo } from '$lib/api';
  import Status from '$lib/Status.svelte';
  import { compareVersions, releaseStatus } from '$lib/releases';

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
        body: JSON.stringify({ confirm: true, target_version: updateTarget })
      });
      await goto(`/jobs/${job.id}`);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      busyAgent = '';
    }
  }

  onMount(load);

  const latest = $derived(system?.release?.version.replace(/^v/, '') || '');
  const updateTarget = $derived(system?.target_version === 'latest' ? latest : system?.target_version || '');
  const controlStatus = $derived(releaseStatus(system?.version || '', latest));
</script>

{#if loading}<div class="loading"></div>{/if}
<section class="page">
  <header class="page-head">
    <div>
      <span class="eyebrow">Release management</span>
      <h1>System</h1>
      <p>Installed versions, latest stable release and agent updates.</p>
    </div>
    <button class="btn" disabled={loading} onclick={load}>Refresh</button>
  </header>
  {#if error}<div class="callout errorbox">{error}</div>{/if}
  {#if system}
    <div class="grid stats">
      <div class="stat" class:attn={controlStatus === 'Update available'}>
        <span class="label">Installed control version</span><strong>{system.version || 'Unknown'}</strong>
        <small>{controlStatus}</small>
      </div>
      <div class="stat">
        <span class="label">Latest stable release</span><strong>{latest || 'Unknown'}</strong>
        <small>
          {#if latest}
            <a
              href={`https://github.com/agugliotta/DockPulse/releases/tag/${system.release.version}`}
              target="_blank"
              rel="noreferrer">Release notes</a
            >
            · checked {since(system.release.checked_at)} · cached up to 5 min
          {:else}Release check unavailable{/if}
        </small>
      </div>
      <div class="stat">
        <span class="label">Agents</span><strong>{system.agents.length}</strong><small
          >{system.agents.filter((a) => !a.read_only).length} writable</small
        >
      </div>
      <div class="stat">
        <span class="label">Online</span><strong
          >{system.agents.filter((a) => a.status !== 'offline').length}</strong
        ><small>Heartbeat observed</small>
      </div>
    </div>
    {#if !latest}
      <div class="callout">
        {system.release?.error || 'Could not check the latest release.'} Installed versions are shown below; update
        status is unknown.
      </div>
    {/if}
    <div class="callout">
      Agent update target: <span class="mono">{updateTarget || 'Unknown'}</span>
      (configured: <span class="mono">{system.target_version}</span>).
      {#if system.target_version === 'latest'}
        The latest published version is used when verified.
      {:else}
        Updates use this pinned target, even if a newer release exists.
      {/if}
      Agent updates require self-update to be enabled on the host.
    </div>
    <div class="callout">
      To update the control server, use its deployment Compose or a local agent configured for the control
      service. Updating the agents below does not automatically update the control server.
    </div>
    <section class="panel">
      <div class="panel-head">
        <div>
          <h2>Agent self-updates</h2>
          <span
            >Update each agent to the target shown above. Writable mode alone does not enable self-update.</span
          >
        </div>
      </div>
      <div class="table-wrap">
        <table>
          <thead
            ><tr
              ><th>Agent</th><th>Installed version</th><th>Release status</th><th>Health</th><th>Mode</th><th
                >Action</th
              ></tr
            ></thead
          >
          <tbody>
            {#each system.agents as agent}
              {@const targetComparison = compareVersions(agent.version, updateTarget)}
              {@const agentReleaseStatus = releaseStatus(agent.version, latest)}
              <tr>
                <td
                  ><a class="name" href={`/agents/${agent.id}`}>{agent.name}</a><span class="sub"
                    >{agent.id}</span
                  ></td
                >
                <td>
                  <span class="version-token">{agent.version || 'Unknown'}</span>
                </td>
                <td
                  ><Status
                    value={agentReleaseStatus === 'Up to date' ? 'healthy' : agentReleaseStatus}
                    label={agentReleaseStatus}
                  /></td
                >
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
                    disabled={agent.status === 'offline' ||
                      agent.read_only ||
                      busyAgent !== '' ||
                      !updateTarget ||
                      targetComparison === 0 ||
                      (targetComparison !== null && targetComparison > 0)}
                    onclick={() => selfUpdate(agent.id)}
                    >{busyAgent === agent.id
                      ? 'Starting...'
                      : targetComparison === 0
                        ? 'At target version'
                        : targetComparison !== null && targetComparison > 0
                          ? 'Target is older'
                          : updateTarget
                            ? `Update to ${updateTarget}`
                            : 'Version unverified'}</button
                  >
                  {#if agent.status === 'offline'}<span class="sub">Agent offline</span>
                  {:else if agent.read_only}<span class="sub">Read-only mode blocks updates</span>{/if}
                </td>
              </tr>
            {:else}
              <tr><td colspan="6" class="empty">No agents registered yet.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    </section>
  {/if}
</section>

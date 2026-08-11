<script lang="ts">
  import { onMount } from 'svelte';
  import { api, imageVersion, short, type Container } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let containers = $state<Container[]>([]);
  let query = $state('');
  let filter = $state('all');
  let loading = $state(true);
  let error = $state('');
  onMount(async () => {
    try {
      containers = await api<Container[]>('/containers');
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  });
  const shown = $derived(
    containers.filter(
      (c) =>
        (!query || `${c.name} ${c.image} ${c.compose_project}`.toLowerCase().includes(query.toLowerCase())) &&
        (filter === 'all' ||
          (filter === 'updates' && c.update_available) ||
          (filter === 'protected' && (c.protected || c.sensitive)) ||
          (filter === 'ignored' && c.ignored))
    )
  );
</script>

{#if loading}<div class="loading"></div>{/if}
<section class="page">
  <header class="page-head">
    <div>
      <span class="eyebrow">Fleet inventory</span>
      <h1>Containers</h1>
      <p>Runtime state, image provenance and management policy.</p>
    </div>
  </header>
  <div class="toolbar">
    <input class="search" bind:value={query} placeholder="Search container, image or stack…" /><select
      class="btn"
      bind:value={filter}
      ><option value="all">All workloads</option><option value="updates">Updates available</option><option
        value="protected">Protected</option
      ><option value="ignored">Ignored</option></select
    >
  </div>
  {#if error}<div class="callout errorbox">{error}</div>{/if}
  <section class="panel">
    <div class="panel-head">
      <h2>Inventory</h2>
      <span>{shown.length} of {containers.length}</span>
    </div>
    <div class="table-wrap">
      <table>
        <thead
          ><tr><th>Workload</th><th>Agent</th><th>Runtime</th><th>Digest path</th><th>Management</th></tr
          ></thead
        ><tbody
          >{#each shown as c}<tr
              ><td
                ><a class="name" href={`/containers/${encodeURIComponent(c.id)}`}>{c.name}</a><span
                  class="sub">{c.image}</span
                ></td
              ><td><a href={`/agents/${c.agent_id}`} class="mono">{c.agent_id}</a></td><td
                ><Status value={c.runtime_status} /></td
              ><td>
                <div class="version-flow">
                  <strong>{imageVersion(c)}</strong>
                  <div class="path">
                    <span class="version-token">local {short(c.current_digest)}</span><span class="arrow"
                      >-&gt;</span
                    ><span class="version-token target">registry {short(c.remote_digest)}</span>
                  </div>
                  <span class="sub"
                    >{c.update_available ? 'Registry digest changed' : 'No digest change'}</span
                  >
                  {#if c.update_available}<Status
                      value="update-available"
                      label="Digest changes"
                    />{:else}<Status value="healthy" label="Current digest" />{/if}
                </div>
              </td><td
                >{#if c.ignored}<Status value="ignored" />{:else if c.protected || c.sensitive}<Status
                    value="protected"
                  />{:else if c.manageable}<Status value="healthy" label="Managed" />{:else}<Status
                    value="degraded"
                    label="Review"
                  />{/if}<span class="sub">{c.management_kind}</span></td
              ></tr
            >{:else}<tr><td colspan="5" class="empty">No containers match this view.</td></tr>{/each}</tbody
        >
      </table>
    </div>
  </section>
</section>

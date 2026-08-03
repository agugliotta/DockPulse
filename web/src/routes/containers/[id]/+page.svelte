<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import {
    api,
    imageVersion,
    parsePlan,
    short,
    since,
    type Container,
    type Job,
    type UpdatePreflight
  } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let item = $state<Container | null>(null);
  let preflight = $state<UpdatePreflight | null>(null);
  let loading = $state(true);
  let error = $state('');
  let modal = $state('');
  let modalText = $state('');
  let scope = $state('service');
  let dryRunPlan = $state<ReturnType<typeof parsePlan>>(null);
  let dryRunScope = $state('');
  const dryRunReviewed = $derived(Boolean(dryRunPlan && dryRunScope === scope));
  const updateReady = $derived(Boolean(preflight?.can_update && dryRunReviewed));
  const blockers = $derived(preflight?.checks.filter((check) => check.status === 'block') ?? []);
  async function load() {
    loading = true;
    try {
      const loaded = await api<Container>(`/containers/${page.params.id}`);
      item = loaded;
      scope = loaded.management_kind === 'compose' ? 'service' : 'container';
      dryRunPlan = null;
      dryRunScope = '';
      await loadPreflight(scope);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
  async function loadPreflight(nextScope = scope) {
    preflight = await api<UpdatePreflight>(
      `/containers/${page.params.id}/preflight?scope=${encodeURIComponent(nextScope)}`
    );
  }
  async function changeScope(nextScope: string) {
    scope = nextScope;
    dryRunPlan = null;
    dryRunScope = '';
    error = '';
    try {
      await loadPreflight(nextScope);
    } catch (e) {
      error = (e as Error).message;
    }
  }
  async function flag(action: string) {
    loading = true;
    try {
      item = await api<Container>(`/containers/${page.params.id}/${action}`, { method: 'POST', body: '{}' });
      await loadPreflight(scope);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
  async function flagStack(action: string) {
    if (!item?.compose_project) return;
    loading = true;
    try {
      await api(`/stacks/${item.agent_id}/${encodeURIComponent(item.compose_project)}/${action}`, {
        method: 'POST',
        body: '{}'
      });
      await load();
    } catch (e) {
      error = (e as Error).message;
      loading = false;
    }
  }
  async function dry() {
    loading = true;
    error = '';
    try {
      const j = await api<Job>(`/containers/${page.params.id}/dry-run`, {
        method: 'POST',
        body: JSON.stringify({ scope })
      });
      modal = 'Dry-run plan';
      modalText = j.dry_run_plan || 'Plan accepted';
      dryRunPlan = parsePlan(j.dry_run_plan);
      dryRunScope = scope;
      await loadPreflight(scope);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
  async function update() {
    if (!updateReady) {
      modal = 'Update not ready';
      modalText = blockers.length
        ? blockers.map((check) => `${check.label}: ${check.message}`).join('\n')
        : 'Run and review Inspect dry run before confirming this update.';
      return;
    }
    loading = true;
    error = '';
    try {
      const j = await api<Job>(`/containers/${page.params.id}/update`, {
        method: 'POST',
        body: JSON.stringify({ scope, confirm: true })
      });
      await goto(`/jobs/${j.id}`);
    } catch (e) {
      error = (e as Error).message;
      modal = '';
    } finally {
      loading = false;
    }
  }
  onMount(load);
</script>

{#if loading}<div class="loading"></div>{/if}
<section class="page">
  {#if item}<header class="page-head">
      <div>
        <span class="eyebrow">{item.management_kind} workload</span>
        <h1>{item.name}</h1>
        <p>{item.image} · synced {since(item.last_sync)}</p>
      </div>
      <div class="actions">
        {#if item.update_available}<Status value="update-available" label="Update available" />{:else}<Status
            value="healthy"
            label="Current"
          />{/if}
      </div>
    </header>
    {#if error}<div class="callout errorbox">{error}</div>{/if}{#if item.safety_reason}<div class="callout">
        Policy note: {item.safety_reason}
      </div>{/if}
    <div class="grid stats">
      <div class="stat">
        <span class="label">Service version path</span><strong
          >{imageVersion(item)} -> {imageVersion(item)}</strong
        ><small>Image tag used for pull</small>
      </div>
      <div class="stat attn">
        <span class="label">Digest path</span><strong
          >{short(item.current_digest)} -> {short(item.remote_digest)}</strong
        ><small>{item.update_available ? 'Registry digest changed' : 'No digest change detected'}</small>
      </div>
      <div class="stat">
        <span class="label">Current source</span><strong>{item.management_kind}</strong><small
          >{item.compose_project || item.agent_id}</small
        >
      </div>
      <div class="stat">
        <span class="label">Policy</span><strong>{item.manageable ? 'Managed' : 'Review'}</strong><small
          >{item.ignored || item.protected ? 'Blocked by flag' : 'Ready for dry-run'}</small
        >
      </div>
    </div>
    <div class="detail-grid">
      <section class="panel">
        <div class="panel-head">
          <h2>Image & runtime</h2>
          <span>{item.runtime_status}</span>
        </div>
        <dl class="detail-list">
          <dt>Image reference</dt>
          <dd class="mono">{item.image}</dd>
          <dt>Local digest</dt>
          <dd class="mono">
            {short(item.current_digest)} <span class="sub">{item.current_digest || 'Unavailable'}</span>
          </dd>
          <dt>Registry digest</dt>
          <dd class="mono">
            {short(item.remote_digest)} <span class="sub">{item.remote_digest || 'Not evaluated'}</span>
          </dd>
          <dt>Detection</dt>
          <dd>{item.detection_method || 'No registry result'}</dd>
          <dt>Docker ID</dt>
          <dd class="mono">{item.docker_id}</dd>
          <dt>Agent</dt>
          <dd><a href={`/agents/${item.agent_id}`}>{item.agent_id}</a></dd>
        </dl>
      </section>
      <section class="panel">
        <div class="panel-head"><h2>Management policy</h2></div>
        <dl class="detail-list">
          <dt>Manageable</dt>
          <dd>{item.manageable ? 'Yes' : 'No'}</dd>
          <dt>Sensitive</dt>
          <dd>{item.sensitive ? 'Yes · explicit opt-in required' : 'No'}</dd>
          <dt>Ignored</dt>
          <dd>{item.ignored ? 'Yes' : 'No'}</dd>
          <dt>Protected</dt>
          <dd>{item.protected ? 'Yes' : 'No'}</dd>
          <dt>Origin</dt>
          <dd>{item.management_kind}</dd>
          {#if item.compose_project}<dt>Stack</dt>
            <dd>{item.compose_project} / {item.compose_service}</dd>
            <dt>Working dir</dt>
            <dd class="mono">{item.compose_working_dir}</dd>{/if}
        </dl>
      </section>
    </div>
    <section class="panel" style="margin-top:16px">
      <div class="panel-head">
        <div>
          <h2>Update readiness</h2>
          <span>Preflight checks for the selected scope</span>
        </div>
      </div>
      <div class="readiness">
        <div class="readiness-flow">
          <div class:ready={Boolean(preflight)}>
            <span>1</span><strong>Preflight</strong><small>Rules checked</small>
          </div>
          <div class:ready={dryRunReviewed}>
            <span>2</span><strong>Dry-run</strong><small>{dryRunReviewed ? 'Reviewed' : 'Required'}</small>
          </div>
          <div class:ready={updateReady}>
            <span>3</span><strong>Update</strong><small>{updateReady ? 'Ready' : 'Blocked'}</small>
          </div>
        </div>
        {#if preflight}
          <div class="checklist">
            {#each preflight.checks as check}
              <div class="check {check.status}">
                <Status value={check.status} label={check.status} />
                <div><strong>{check.label}</strong><span>{check.message}</span></div>
              </div>
            {/each}
          </div>
        {/if}
        {#if !dryRunReviewed}
          <div class="callout">Run Inspect dry run for this scope before Update now becomes available.</div>
        {/if}
        <div class="operation-actions">
          {#if item.management_kind === 'compose'}<select
              class="btn"
              value={scope}
              onchange={(e) => changeScope(e.currentTarget.value)}
              ><option value="service">This service</option><option value="stack">Entire stack</option
              ></select
            >{/if}<button class="btn" disabled={!preflight?.can_dry_run} onclick={dry}>Inspect dry run</button
          ><button
            class="btn primary"
            disabled={!updateReady}
            onclick={() => {
              modal = 'Confirm update';
              modalText = `DockPulse will execute the reviewed ${scope} update on ${item?.name}. Persistent data mounts are preserved, but application-level rollback is not guaranteed.`;
            }}>Update now</button
          ><button class="btn" onclick={() => flag(item?.ignored ? 'unignore' : 'ignore')}
            >{item.ignored ? 'Unignore' : 'Ignore'}</button
          ><button class="btn" onclick={() => flag(item?.protected ? 'unprotect' : 'protect')}
            >{item.protected ? 'Unprotect' : 'Protect'}</button
          >
          {#if item.management_kind === 'compose'}<button
              class="btn"
              onclick={() => flagStack(item?.ignored ? 'unignore' : 'ignore')}
              >{item.ignored ? 'Unignore stack' : 'Ignore stack'}</button
            >{/if}
        </div>
      </div>
    </section>{/if}
</section>
{#if modal}<div
    class="modal-backdrop"
    role="presentation"
    onclick={(e) => {
      if (e.target === e.currentTarget) modal = '';
    }}
  >
    <div class="modal" role="dialog" aria-modal="true">
      <h2>{modal}</h2>
      {#if modal === 'Dry-run plan'}
        <div class="callout">
          Dry-run is an inspection only: DockPulse validated what it would do, but did not change Docker.
        </div>
        {#if dryRunPlan}
          <div class="plan">
            <div class="plan-summary">
              <div><span>Target</span><strong>{dryRunPlan.target}</strong></div>
              <div><span>Scope</span><strong>{dryRunPlan.scope}</strong></div>
            </div>
            <ol class="plan-steps">
              {#each dryRunPlan.steps as step}<li>{step}</li>{/each}
            </ol>
            {#if dryRunPlan.warnings?.length}
              <div class="plan-warnings">
                {#each dryRunPlan.warnings as warning}<div class="callout">{warning}</div>{/each}
              </div>
            {/if}
          </div>
        {:else}
          <pre>{modalText}</pre>
        {/if}
      {:else if modal === 'Update not ready'}<pre>{modalText}</pre>
        <div class="callout">
          Fix the blocked checks or run a fresh dry-run for the selected scope.
        </div>{:else}<p>{modalText}</p>
        <div class="callout">
          This action recreates runtime state using the dry-run you just reviewed.
        </div>{/if}
      <div class="actions" style="justify-content:flex-end;margin-top:16px">
        <button class="btn" onclick={() => (modal = '')}>Cancel</button
        >{#if modal === 'Confirm update'}<button class="btn danger" onclick={update}>Confirm update</button
          >{/if}
      </div>
    </div>
  </div>{/if}

<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { api, parsePlan, short, since, type Container, type Job, type UpdatePreflight } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let item = $state<Container | null>(null);
  let preflight = $state<UpdatePreflight | null>(null);
  let loading = $state(true);
  let error = $state('');
  let scope = $state('service');
  let dryRunPlan = $state<ReturnType<typeof parsePlan>>(null);
  let dryRunText = $state('');
  let dryRunScope = $state('');
  let dryRunOpen = $state(false);
  const previewSeen = $derived(Boolean((dryRunPlan || dryRunText) && dryRunScope === scope));
  const updateReady = $derived(Boolean(preflight?.can_update));
  const blockers = $derived(preflight?.checks.filter((check) => check.status === 'block') ?? []);
  const updateState = $derived(
    !preflight ? 'loading' : !preflight.can_update ? 'preflight-blocked' : 'ready'
  );
  const updateHint = $derived(
    updateState === 'ready'
      ? 'Preflight passed. Update is ready for the selected scope.'
      : blockers.length
        ? blockers[0].message
        : 'Preflight still blocks this update.'
  );
  const previewHint = $derived(
    previewSeen
      ? 'Preview is advisory. Runtime conditions can still change before the update runs.'
      : 'Preview changes is optional. It does not gate Update now.'
  );
  async function load() {
    loading = true;
    try {
      const loaded = await api<Container>(`/containers/${page.params.id}`);
      item = loaded;
      scope = loaded.management_kind === 'compose' ? 'service' : 'container';
      dryRunPlan = null;
      dryRunText = '';
      dryRunScope = '';
      dryRunOpen = false;
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
    dryRunText = '';
    dryRunScope = '';
    dryRunOpen = false;
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
      dryRunText = j.dry_run_plan || 'Plan accepted';
      dryRunPlan = parsePlan(j.dry_run_plan);
      dryRunScope = scope;
      dryRunOpen = true;
      await loadPreflight(scope);
    } catch (e) {
      error = (e as Error).message;
    } finally {
      loading = false;
    }
  }
  async function update() {
    if (!updateReady) {
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
      <div class="stat attn">
        <span class="label">Update posture</span><strong
          >{item.update_available ? 'Actionable' : 'Current'}</strong
        ><small>{item.update_available ? 'Registry changed' : 'No image change detected'}</small>
      </div>
      <div class="stat">
        <span class="label">Source</span><strong
          >{item.management_kind === 'compose' ? 'Compose' : 'Container'}</strong
        ><small>{item.compose_project || item.agent_id}</small>
      </div>
      <div class="stat">
        <span class="label">Policy</span><strong>{item.manageable ? 'Managed' : 'Review'}</strong><small
          >{item.ignored || item.protected ? 'Flagged by policy' : 'Eligible for preview'}</small
        >
      </div>
      <div class="stat">
        <span class="label">Runtime</span><strong>{item.runtime_status}</strong><small
          >Synced {since(item.last_sync)}</small
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
          <dt>Agent</dt>
          <dd><a href={`/agents/${item.agent_id}`}>{item.agent_id}</a></dd>
        </dl>
        <details class="tech-fold">
          <summary>Show technical signals</summary>
          <div class="tech-grid">
            <div>
              <span>Local digest</span>
              <strong class="mono">{short(item.current_digest)}</strong>
              <small>{item.current_digest || 'Unavailable'}</small>
            </div>
            <div>
              <span>Registry digest</span>
              <strong class="mono">{short(item.remote_digest)}</strong>
              <small>{item.remote_digest || 'Not evaluated'}</small>
            </div>
            <div>
              <span>Docker ID</span>
              <strong class="mono">{item.docker_id}</strong>
            </div>
            <div>
              <span>Detection</span>
              <strong>{item.detection_method || 'No registry result'}</strong>
            </div>
          </div>
        </details>
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
    <section class="panel readiness-panel" style="margin-top:16px">
      <div class="panel-head">
        <div>
          <h2>Update readiness</h2>
          <span>Preflight checks, optional preview and the final update action</span>
        </div>
      </div>
      <div class="readiness">
        <div class="readiness-summary">
          <div class:ready={Boolean(preflight?.can_update)}>
            <strong>1</strong>
            <span>Preflight</span>
            <small>{preflight?.can_update ? 'Checks passed' : 'Review blockers'}</small>
          </div>
          <div class:ready={previewSeen}>
            <strong>2</strong>
            <span>Preview</span>
            <small>{previewSeen ? 'Open' : 'Optional'}</small>
          </div>
          <div class:ready={updateReady}>
            <strong>3</strong>
            <span>Update</span>
            <small>{updateReady ? 'Ready' : 'Locked'}</small>
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
        <div class="readiness-note">
          <div class="callout">{updateHint}</div>
          <span class="sub">{previewHint}</span>
        </div>
        <div class="operation-actions">
          {#if item.management_kind === 'compose'}<select
              class="btn"
              value={scope}
              onchange={(e) => changeScope(e.currentTarget.value)}
              aria-label="Update scope"
              ><option value="service">This service</option><option value="stack">Entire stack</option
              ></select
            >{/if}<button
            class="btn"
            disabled={!preflight?.can_dry_run}
            title={!preflight?.can_dry_run
              ? 'Run preflight checks before previewing changes.'
              : 'Optional preview; does not block Update now.'}
            onclick={dry}
          >
            Preview changes
          </button><button class="btn primary" disabled={!updateReady} title={updateHint} onclick={update}>
            Update now
          </button>
          <details class="menu">
            <summary class="btn">More actions</summary>
            <div class="menu-panel">
              <button class="btn" onclick={() => flag(item?.ignored ? 'unignore' : 'ignore')}
                >{item.ignored ? 'Unignore' : 'Ignore'}</button
              >
              <button class="btn" onclick={() => flag(item?.protected ? 'unprotect' : 'protect')}
                >{item.protected ? 'Unprotect' : 'Protect'}</button
              >
              {#if item.management_kind === 'compose'}<button
                  class="btn"
                  onclick={() => flagStack(item?.ignored ? 'unignore' : 'ignore')}
                  >{item.ignored ? 'Unignore stack' : 'Ignore stack'}</button
                >{/if}
            </div>
          </details>
        </div>
        {#if dryRunOpen}
          <div class="dry-run-panel">
            <div class="panel-head dry-run-head">
              <div>
                <h3>Preview changes</h3>
                <span>Optional inspection. It does not gate Update now.</span>
              </div>
              <button class="btn" onclick={() => (dryRunOpen = false)}>Hide preview</button>
            </div>
            {#if dryRunPlan}
              <div class="plan">
                <div class="plan-summary">
                  <div>
                    <span>Target</span>
                    <strong>{dryRunPlan.target}</strong>
                  </div>
                  <div>
                    <span>Scope</span>
                    <strong>{dryRunPlan.scope}</strong>
                  </div>
                </div>
                <ol class="plan-steps">
                  {#each dryRunPlan.steps as step}
                    <li>{step}</li>
                  {/each}
                </ol>
                {#if dryRunPlan.warnings?.length}
                  <div class="plan-warnings">
                    {#each dryRunPlan.warnings as warning}
                      <div class="callout">{warning}</div>
                    {/each}
                  </div>
                {/if}
              </div>
            {:else}
              <pre>{dryRunText || 'Preview accepted.'}</pre>
            {/if}
            <div class="dry-run-actions">
              <div class="callout" style="margin:0">
                This snapshot can help you understand the change, but it is not a guarantee that the live
                update will succeed.
              </div>
            </div>
          </div>
        {/if}
      </div>
    </section>{/if}
</section>

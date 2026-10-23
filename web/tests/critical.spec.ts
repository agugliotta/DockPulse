import { expect, test } from '@playwright/test';

const agent = {
  id: 'lxc-101',
  name: 'media-lxc',
  base_url: 'http://10.0.0.101:9090',
  version: '0.1.0',
  read_only: false,
  status: 'healthy',
  last_heartbeat: new Date().toISOString(),
  last_sync: new Date().toISOString()
};
const container = {
  id: 'lxc-101:abc',
  agent_id: 'lxc-101',
  docker_id: 'abc',
  name: 'immich-server',
  image: 'ghcr.io/immich-app/immich-server:v1.2',
  tag: 'v1.2',
  current_digest: 'sha256:aaa',
  remote_digest: 'sha256:bbb',
  update_available: true,
  detection_method: 'registry-manifest-digest',
  runtime_status: 'running',
  management_kind: 'compose',
  compose_project: 'immich',
  compose_service: 'server',
  compose_working_dir: '/opt/stacks/immich',
  labels: {},
  ignored: false,
  protected: false,
  sensitive: false,
  manageable: true,
  last_sync: new Date().toISOString()
};
const preflight = {
  container_id: container.id,
  agent_id: container.agent_id,
  scope: 'service',
  can_dry_run: true,
  can_update: true,
  checks: [
    { key: 'scope', label: 'Update scope', status: 'pass', message: 'Scope service is valid.' },
    { key: 'agent_status', label: 'Agent connectivity', status: 'pass', message: 'Agent is online.' },
    { key: 'agent_mode', label: 'Agent mode', status: 'pass', message: 'Agent is writable.' }
  ]
};

test.beforeEach(async ({ page }) => {
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path === '/api/v1/agents') return route.fulfill({ json: [agent] });
    if (path === '/api/v1/containers') return route.fulfill({ json: [container] });
    if (path === '/api/v1/jobs') return route.fulfill({ json: [] });
    if (path === `/api/v1/containers/${container.id}`) return route.fulfill({ json: container });
    if (path === `/api/v1/containers/${container.id}/preflight`) return route.fulfill({ json: preflight });
    if (path.endsWith('/dry-run'))
      return route.fulfill({
        status: 202,
        json: { id: 'job-1', dry_run_plan: 'Pull immutable manifest(s)\nReconcile Compose service' }
      });
    return route.fulfill({ status: 404, json: { error: `unmocked ${path}` } });
  });
});

test('operator sees fleet posture and can preview changes without blocking update', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'System overview' })).toBeVisible();
  const workload = page.getByRole('link', { name: 'immich-server' });
  await expect(workload).toBeVisible();
  await workload.click();
  await expect(page.getByRole('heading', { name: 'immich-server' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Update now' })).toBeEnabled();
  await page.getByRole('button', { name: 'Preview changes' }).click();
  await expect(page.getByRole('heading', { name: 'Preview changes' })).toBeVisible();
  await expect(page.getByText('Reconcile Compose service')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Update now' })).toBeEnabled();
});

test('missing digests show unknown freshness across inventory and details', async ({ page }) => {
  const unknown = { ...container, remote_digest: '', update_available: false };
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/v1/containers' || path === `/api/v1/agents/${agent.id}/containers`)
      return route.fulfill({ json: [unknown] });
    if (path === `/api/v1/containers/${container.id}`) return route.fulfill({ json: unknown });
    if (path === `/api/v1/agents/${agent.id}`) return route.fulfill({ json: agent });
    return route.fallback();
  });
  for (const path of ['/containers', `/containers/${container.id}`, `/agents/${agent.id}`]) {
    await page.goto(path);
    await expect(page.getByText('Freshness unknown', { exact: true })).toBeVisible();
    await expect(page.getByText('Current', { exact: true })).toHaveCount(0);
  }
});

test('zero counts have zero-width analytics bars', async ({ page }) => {
  await page.route('**/api/v1/jobs?*', (route) =>
    route.fulfill({
      json: [
        {
          id: 'job-1',
          action: 'update',
          status: 'succeeded',
          target_key: 'app',
          created_at: new Date().toISOString()
        }
      ]
    })
  );
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Agent health' })).toBeVisible();
  const bars = page.locator('.metric-fill.danger');
  await expect(bars).toHaveCount(2);
  for (const bar of await bars.all()) await expect(bar).toHaveCSS('width', '0px');
});

test('terminal status events refresh the job page', async ({ page }) => {
  let reads = 0;
  const job = {
    id: 'job-1',
    action: 'update',
    status: 'running',
    target_key: 'app',
    requested_by: 'test',
    created_at: new Date().toISOString()
  };
  await page.route('**/api/v1/jobs/job-1**', (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith('/logs')) return route.fulfill({ json: [] });
    if (path.endsWith('/events'))
      return route.fulfill({
        contentType: 'text/event-stream',
        body: `id: 1\nevent: log\ndata: ${JSON.stringify({ id: 1, job_id: job.id, level: 'info', message: 'Job status: succeeded', created_at: job.created_at })}\n\n`
      });
    reads++;
    return route.fulfill({ json: { ...job, status: reads === 1 ? 'running' : 'succeeded' } });
  });
  await page.goto('/jobs/job-1');
  await expect(page.locator('.page-head .status')).toHaveText('succeeded');
});

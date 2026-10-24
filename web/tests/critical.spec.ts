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
    if (path === '/api/v1/health')
      return route.fulfill({ json: { status: 'ok', service: 'dockpulse-control', version: '0.3.3' } });
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

test('sidebar shows the running control version and verifies connectivity', async ({ page }) => {
  let connected = true;
  await page.route('**/api/v1/health', (route) =>
    connected
      ? route.fulfill({ json: { status: 'ok', service: 'dockpulse-control', version: '0.3.5' } })
      : route.fulfill({ status: 503, json: { error: 'Unavailable' } })
  );
  await page.clock.install();
  await page.goto('/');
  const indicator = page.getByRole('link', { name: 'Control connection and installed version' });
  await expect(indicator).toContainText('Control online');
  await expect(indicator).toContainText('0.3.5');
  await expect(indicator).toHaveAttribute('href', '/system');
  connected = false;
  await page.clock.runFor(30000);
  await expect(indicator).toContainText('Control unreachable');
  await expect(indicator).toContainText('Version unavailable');
  await expect(indicator).not.toHaveClass(/online/);
  connected = true;
  await page.clock.runFor(30000);
  await expect(indicator).toContainText('Control online');
  await expect(indicator).toContainText('0.3.5');
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
    if (path === `/containers/${container.id}`) {
      await expect(
        page.getByText('Could not compare the installed image with the registry.', { exact: false })
      ).toBeVisible();
    }
  }
});

test('image details explain the comparison and keep diagnostic IDs readable on desktop and mobile', async ({
  page
}) => {
  let matches = false;
  const local = `sha256:${'a'.repeat(64)}`;
  const remote = `sha256:${'b'.repeat(64)}`;
  const dockerID = 'c'.repeat(64);
  await page.route(`**/api/v1/containers/${container.id}`, (route) =>
    route.fulfill({
      json: {
        ...container,
        current_digest: local,
        remote_digest: matches ? local : remote,
        update_available: !matches,
        docker_id: dockerID
      }
    })
  );
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    await page.goto(`/containers/${container.id}`);
    const card = page
      .locator('section.panel')
      .filter({ has: page.getByRole('heading', { name: 'Image & runtime' }) });
    await expect(
      card.getByText('The registry image differs from the installed image.', { exact: false })
    ).toBeVisible();
    const summary = card.locator('summary');
    const fingerprint = card.getByText(local, { exact: true });
    await expect(fingerprint).toBeHidden();
    const cardBox = await card.boundingBox();
    const summaryBox = await summary.boundingBox();
    expect(summaryBox!.x - cardBox!.x).toBeGreaterThanOrEqual(16);
    await summary.click();
    await expect(fingerprint).toBeVisible();
    await expect(card.getByText(dockerID, { exact: true })).toBeVisible();
    await expect(card.getByText('registry-manifest-digest', { exact: true })).toHaveCount(0);
    expect(await card.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    await summary.click();
    await expect(fingerprint).toBeHidden();
  }
  matches = true;
  await page.reload();
  await expect(
    page.getByText('The installed image matches the registry image at the last check.', { exact: true })
  ).toBeVisible();
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

test('browser tab loads the DockPulse favicon', async ({ page, request }) => {
  await page.goto('/');
  const icon = page.locator('link[rel="icon"]');
  await expect(icon).toHaveAttribute('type', 'image/svg+xml');
  const href = await icon.getAttribute('href');
  expect(href).toBeTruthy();
  const response = await request.get(href!);
  expect(response.ok()).toBeTruthy();
  expect(response.headers()['content-type']).toContain('image/svg+xml');
  expect(await response.text()).toContain('<svg');
});

test('system distinguishes installed versions, stable release and update actions', async ({ page }) => {
  await page.route('**/api/v1/system', (route) =>
    route.fulfill({
      json: {
        version: '0.3.3',
        target_version: 'latest',
        release: { version: 'v0.3.5', checked_at: new Date().toISOString() },
        agents: [
          { ...agent, version: '0.3.3' },
          { ...agent, id: 'current', name: 'current-agent', version: 'v0.3.5' },
          { ...agent, id: 'ahead', name: 'ahead-agent', version: '0.3.10' },
          { ...agent, id: 'dev', name: 'dev-agent', version: 'dev', read_only: true },
          { ...agent, id: 'offline', name: 'offline-agent', version: '0.3.3', status: 'offline' }
        ]
      }
    })
  );
  await page.goto('/system');
  await expect(page.locator('.stat').filter({ hasText: 'Installed control version' })).toContainText('0.3.3');
  await expect(page.locator('.stat').filter({ hasText: 'Installed control version' })).toContainText(
    'Update available'
  );
  await expect(page.getByRole('link', { name: 'Release notes' })).toHaveAttribute(
    'href',
    'https://github.com/agugliotta/DockPulse/releases/tag/v0.3.5'
  );
  const old = page.getByRole('row').filter({ hasText: 'media-lxc' });
  await expect(old).toContainText('Update available');
  await expect(old.getByRole('button', { name: 'Update to 0.3.5' })).toBeEnabled();
  await expect(page.getByRole('row').filter({ hasText: 'current-agent' })).toContainText('Up to date');
  await expect(page.getByRole('button', { name: 'At target version' })).toBeDisabled();
  await expect(page.getByRole('row').filter({ hasText: 'ahead-agent' })).toContainText(
    'Ahead of stable release'
  );
  await expect(page.getByRole('button', { name: 'Target is older' })).toBeDisabled();
  for (const name of ['dev-agent', 'offline-agent']) {
    await expect(page.getByRole('row').filter({ hasText: name }).getByRole('button')).toBeDisabled();
  }
  await page.route(`**/api/v1/agents/${agent.id}/self-update`, async (route) => {
    expect(route.request().postDataJSON()).toEqual({ confirm: true, target_version: '0.3.5' });
    await route.fulfill({ status: 409, json: { error: 'Self-update is disabled on this host' } });
  });
  await old.getByRole('button').click();
  await expect(page.getByText('Self-update is disabled on this host')).toBeVisible();
});

test('system does not claim freshness when the release check fails', async ({ page }) => {
  await page.route('**/api/v1/system', (route) =>
    route.fulfill({
      json: {
        version: '0.3.3',
        target_version: 'latest',
        agents: [{ ...agent, version: '0.3.3' }],
        release: {
          version: '',
          checked_at: new Date().toISOString(),
          error: 'Could not check the latest release. Try again shortly.'
        }
      }
    })
  );
  await page.goto('/system');
  await expect(page.getByText('Could not check the latest release.', { exact: false })).toBeVisible();
  await expect(page.getByText('Up to date', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Version unverified' })).toBeDisabled();
});

test('system respects a pinned target even when a newer release exists', async ({ page }) => {
  await page.route('**/api/v1/system', (route) =>
    route.fulfill({
      json: {
        version: '0.3.3',
        target_version: '0.3.3',
        agents: [{ ...agent, version: '0.3.3' }],
        release: { version: 'v0.3.5', checked_at: new Date().toISOString() }
      }
    })
  );
  await page.goto('/system');
  await expect(page.getByRole('row').filter({ hasText: 'media-lxc' })).toContainText('Update available');
  await expect(page.getByRole('button', { name: 'At target version' })).toBeDisabled();
  await expect(page.getByText('Updates use this pinned target', { exact: false })).toBeVisible();
});

test('manager allows and blocks sensitive updates without changing agent files', async ({ page }) => {
  let allowed = false;
  let failSave = false;
  const dataService = () => ({ ...container, sensitive: true, sensitive_updates_allowed: allowed });
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === `/api/v1/containers/${container.id}`) return route.fulfill({ json: dataService() });
    if (path === `/api/v1/containers/${container.id}/preflight`)
      return route.fulfill({
        json: {
          ...preflight,
          can_update: allowed,
          can_dry_run: allowed,
          checks: [
            {
              key: 'sensitive',
              label: 'Data service permission',
              status: allowed ? 'warn' : 'block',
              message: allowed ? 'Updates allowed' : 'Enable updates in Management policy'
            }
          ]
        }
      });
    if (path.endsWith('/allow-sensitive') || path.endsWith('/block-sensitive')) {
      if (failSave) return route.fulfill({ status: 500, json: { error: 'Could not save policy' } });
      allowed = path.endsWith('/allow-sensitive');
      return route.fulfill({ json: dataService() });
    }
    if (path === '/api/v1/containers') return route.fulfill({ json: [dataService()] });
    return route.fallback();
  });
  await page.goto(`/containers/${container.id}`);
  const toggle = page.getByRole('switch', { name: 'Allow updates for this data service' });
  const update = page.getByRole('button', { name: 'Update now', exact: true });
  await expect(toggle).toHaveAttribute('aria-checked', 'false');
  await expect(page.locator('.stat').filter({ hasText: 'Policy' })).toContainText('Blocked');
  await expect(update).toBeDisabled();
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-checked', 'true');
  await expect(page.locator('.stat').filter({ hasText: 'Policy' })).toContainText('Managed');
  await expect(update).toBeEnabled();
  await page.reload();
  await expect(toggle).toHaveAttribute('aria-checked', 'true');
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-checked', 'false');
  await expect(update).toBeDisabled();
  failSave = true;
  await toggle.click();
  await expect(page.getByText('Could not save policy', { exact: true })).toBeVisible();
  await expect(toggle).toHaveAttribute('aria-checked', 'false');
  await expect(update).toBeDisabled();
  await page.goto('/containers');
  await expect(page.getByText('Updates blocked', { exact: true })).toBeVisible();
  allowed = true;
  await page.reload();
  await expect(page.getByText('Updates allowed', { exact: true })).toBeVisible();
});

<script lang="ts">
  import { page } from '$app/state';
  import { onMount } from 'svelte';
  import { api } from '$lib/api';

  let connection = $state('Checking control');
  let version = $state('');

  onMount(() => {
    let stopped = false;
    let request: AbortController;
    async function checkControl() {
      request = new AbortController();
      const timeout = setTimeout(() => request.abort(), 5000);
      try {
        const health = await api<{ status: string; service: string; version: string }>('/health', {
          signal: request.signal,
          cache: 'no-store'
        });
        if (health.status !== 'ok' || health.service !== 'dockpulse-control')
          throw new Error('Invalid health response');
        if (!stopped) {
          connection = 'Control online';
          version = health.version || '';
        }
      } catch {
        if (!stopped) {
          connection = 'Control unreachable';
          version = '';
        }
      } finally {
        clearTimeout(timeout);
      }
    }
    void checkControl();
    const interval = setInterval(checkControl, 30000);
    return () => {
      stopped = true;
      clearInterval(interval);
      request.abort();
    };
  });
  const links = [
    ['/', 'Overview'],
    ['/agents', 'Agents'],
    ['/containers', 'Inventory'],
    ['/jobs', 'Activity'],
    ['/system', 'System']
  ];
</script>

<aside class="sidebar">
  <a class="brand" href="/"
    ><span class="mark"><i></i></span><span>DockPulse<small>CONTROL PLANE</small></span></a
  >
  <nav>
    {#each links as link}<a
        class:active={page.url.pathname === link[0] ||
          (link[0] !== '/' && page.url.pathname.startsWith(link[0]))}
        href={link[0]}><span>{link[1]}</span></a
      >{/each}
  </nav>
  <div class="sidebar-foot">
    <a
      class="connection"
      class:online={connection === 'Control online'}
      class:unreachable={connection === 'Control unreachable'}
      href="/system"
      aria-label="Control connection and installed version"
      title="Connection checked every 30 seconds. Open System for release details."
    >
      <i></i>
      <span aria-live="polite"
        >{connection}<small
          >{version ||
            (connection === 'Checking control' ? 'Checking version...' : 'Version unavailable')}</small
        ></span
      >
    </a>
    <a
      href="https://github.com/agugliotta/DockPulse"
      target="_blank"
      rel="noreferrer"
      aria-label="Open DockPulse repository on GitHub"
    >
      GitHub repository
    </a>
  </div>
</aside>

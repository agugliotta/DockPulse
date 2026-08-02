<script lang="ts">
  import { page } from '$app/state';
  const links = [
    ['/', 'Overview'],
    ['/agents', 'Agents'],
    ['/containers', 'Inventory'],
    ['/jobs', 'Activity']
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
    <span class="connection"><i></i> Control online</span><small>v0.1.0 · MVP</small>
  </div>
</aside>

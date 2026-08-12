<script lang="ts">
  import '../app.css';
  import Nav from '$lib/Nav.svelte';
  let { children } = $props();
</script>

<div class="shell">
  <Nav />
  <main class="app">{@render children()}</main>
</div>

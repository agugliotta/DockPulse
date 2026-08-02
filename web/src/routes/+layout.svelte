<script lang="ts">
  import '../app.css';
  import Nav from '$lib/Nav.svelte';
  let { children } = $props();
</script>

<Nav />
<main class="app">{@render children()}</main>

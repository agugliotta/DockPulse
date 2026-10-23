<script lang="ts">
  import type { Container } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let { container }: { container: Container } = $props();
  const known = $derived(Boolean(container.current_digest && container.remote_digest));
</script>

<Status
  value={!known ? 'degraded' : container.update_available ? 'update-available' : 'healthy'}
  label={!known ? 'Freshness unknown' : container.update_available ? 'Update available' : 'Current'}
/>

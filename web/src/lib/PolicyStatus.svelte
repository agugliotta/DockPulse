<script lang="ts">
  import { allowsSensitiveUpdates, type Container } from '$lib/api';
  import Status from '$lib/Status.svelte';
  let { container: c }: { container: Container } = $props();
</script>

{#if c.ignored}<Status value="ignored" />
{:else if c.protected}<Status value="protected" />
{:else if c.sensitive && !allowsSensitiveUpdates(c)}<Status value="warn" label="Updates blocked" /><span
    class="sub">Data service · enable in Management policy</span
  >
{:else if !c.manageable}<Status value="warn" label="Review" />
{:else if c.sensitive}<Status value="healthy" label="Updates allowed" /><span class="sub">Data service</span>
{:else}<Status value="healthy" label="Eligible" />{/if}

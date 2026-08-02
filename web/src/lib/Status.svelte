<script lang="ts">
  let { value, label = '' }: { value: string; label?: string } = $props();
  const normalized = $derived(value?.toLowerCase().replaceAll(' ', '-') || 'unknown');
</script>

<span class="status {normalized}"><i></i>{label || value}</span>

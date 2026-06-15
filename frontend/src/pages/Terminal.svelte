<script lang="ts">
  import TermView from '../components/TermView.svelte';
  const host = window.location.host;
</script>

<div class="page">
  <TermView wsPath="/ws/terminal" title="Terminal" subtitle={host} />
</div>

<style>
  .page { height: var(--page-h); }
</style>

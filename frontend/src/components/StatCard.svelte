<script lang="ts">
  export let label: string;
  export let value: string | number;
  export let unit = '';
  export let threshold: 'normal' | 'warn' | 'critical' = 'normal';

  $: color =
    threshold === 'critical' ? 'var(--red)' : threshold === 'warn' ? 'var(--peach)' : 'var(--green)';
</script>

<div class="card">
  <span class="label">{label}</span>
  <span class="value" style="--accent: {color}">{value}<small>{unit}</small></span>
</div>

<style>
  .card {
    background: var(--surface);
    border: 1px solid var(--border);
    padding: 1.5rem;
    border-radius: 8px;
    display: flex;
    flex-direction: column;
    justify-content: space-between;
    align-items: center;
    gap: 0.5rem;
  }
  .label {
    color: var(--text-muted);
    font-size: 0.9rem;
    text-transform: uppercase;
    letter-spacing: 1px;
  }
  .value {
    font-size: 1.8rem;
    font-weight: bold;
    color: var(--accent);
    display: flex;
    align-items: baseline;
    gap: 2px;
  }
  small { font-size: 0.6em; opacity: 0.7; }
</style>

<script lang="ts">
  export let value = 0;
  export let max = 100;
  export let label = '';
  export let unit = '%';
  export let display = ''; // optional text override (e.g. "50.1")

  $: frac = Math.max(0, Math.min(1, (value || 0) / max));
  $: color = frac > 0.85 ? 'var(--red)' : frac > 0.6 ? 'var(--peach)' : 'var(--green)';

  const R = 50;
  const LEN = Math.PI * R; // semicircle arc length
  $: dash = `${frac * LEN} ${LEN}`;
</script>

<div class="g">
  <svg viewBox="0 0 120 72">
    <path class="track" d="M10 62 A50 50 0 0 1 110 62" />
    <path class="fill" d="M10 62 A50 50 0 0 1 110 62" style="stroke:{color}" stroke-dasharray={dash} />
    <text x="60" y="58" class="v">{display || Math.round(value || 0)}<tspan class="u">{unit}</tspan></text>
  </svg>
  <div class="lbl">{label}</div>
</div>

<style>
  .g {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.8rem 0.5rem 0.65rem;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.15rem;
  }
  svg { width: 100%; max-width: 150px; display: block; }
  .track { fill: none; stroke: var(--surface-3); stroke-width: 9; stroke-linecap: round; }
  .fill {
    fill: none;
    stroke-width: 9;
    stroke-linecap: round;
    transition: stroke-dasharray 0.45s ease, stroke 0.3s;
  }
  .v {
    fill: var(--text);
    font-size: 22px;
    font-weight: 700;
    text-anchor: middle;
    font-family: 'JetBrains Mono', monospace;
  }
  .u { fill: var(--text-muted); font-size: 12px; }
  .lbl { color: var(--text-muted); font-size: 0.72rem; text-transform: uppercase; letter-spacing: 1px; }
</style>

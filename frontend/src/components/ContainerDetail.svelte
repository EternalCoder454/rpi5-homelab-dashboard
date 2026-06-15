<script lang="ts">
  import { onMount, onDestroy, createEventDispatcher } from 'svelte';
  import Icon from './Icon.svelte';
  import TermView from './TermView.svelte';

  interface C {
    id: string;
    name: string;
    image: string;
    state: string;
    status: string;
    ports: string;
    cpu_percent: number;
    mem_usage: number;
    mem_limit: number;
  }

  export let container: C;

  const dispatch = createEventDispatcher<{ back: void; action: string }>();

  let titles: string[] = [];
  let procs: string[][] = [];
  let topErr = '';
  let timer: ReturnType<typeof setInterval>;

  async function loadTop() {
    if (container.state !== 'running') {
      titles = [];
      procs = [];
      return;
    }
    try {
      const d = await (await fetch('/api/docker/top?id=' + encodeURIComponent(container.id))).json();
      titles = d.titles || [];
      procs = d.processes || [];
      topErr = '';
    } catch (e) {
      topErr = String(e instanceof Error ? e.message : e);
    }
  }

  function fmtBytes(n: number): string {
    if (!n) return '0';
    if (n < 1048576) return (n / 1024).toFixed(0) + ' K';
    if (n < 1073741824) return (n / 1048576).toFixed(0) + ' M';
    return (n / 1073741824).toFixed(1) + ' G';
  }
  function stateColor(s: string): string {
    if (s === 'running') return '#a6e3a1';
    if (s === 'paused' || s === 'restarting') return '#fab387';
    if (s === 'dead') return '#f38ba8';
    return '#6c7086';
  }

  $: memPct = container.mem_limit > 0 ? (container.mem_usage / container.mem_limit) * 100 : 0;

  onMount(() => {
    loadTop();
    timer = setInterval(loadTop, 3000);
  });
  onDestroy(() => clearInterval(timer));
</script>

<div class="detail">
  <header>
    <div class="left">
      <button class="back" on:click={() => dispatch('back')}><Icon name="arrow-left" /> Back</button>
      <span class="dot" style="background:{stateColor(container.state)}"></span>
      <span class="name">{container.name}</span>
      <span class="image">{container.image}</span>
    </div>
    <div class="actions">
      {#if container.state === 'running'}
        <button on:click={() => dispatch('action', 'stop')}><Icon name="square" /> Stop</button>
        <button on:click={() => dispatch('action', 'restart')}><Icon name="refresh" /> Restart</button>
      {:else}
        <button class="ok" on:click={() => dispatch('action', 'start')}><Icon name="play" /> Start</button>
      {/if}
      <button class="danger" on:click={() => dispatch('action', 'remove')}><Icon name="trash" /> Remove</button>
    </div>
  </header>

  <div class="cards">
    <div class="card">
      <span class="label">CPU</span>
      <span class="val" style="color:{container.cpu_percent > 85 ? '#f38ba8' : container.cpu_percent > 50 ? '#fab387' : '#a6e3a1'}">
        {container.state === 'running' ? container.cpu_percent.toFixed(1) + '%' : '—'}
      </span>
    </div>
    <div class="card">
      <span class="label">Memory</span>
      <span class="val" style="color:{memPct > 85 ? '#f38ba8' : memPct > 50 ? '#fab387' : '#a6e3a1'}">
        {container.state === 'running' ? `${fmtBytes(container.mem_usage)} / ${fmtBytes(container.mem_limit)}` : '—'}
      </span>
      {#if container.state === 'running' && container.mem_limit > 0}
        <div class="bar"><span style="width:{Math.min(memPct, 100)}%"></span></div>
      {/if}
    </div>
    <div class="card">
      <span class="label">State</span>
      <span class="val small">{container.status}</span>
    </div>
    <div class="card">
      <span class="label">Ports</span>
      <span class="val small">{container.ports || '—'}</span>
    </div>
  </div>

  <div class="two-col">
    <section class="pane">
      <h2>Processes</h2>
      <div class="pane-body scroll">
        {#if container.state !== 'running'}
          <p class="muted">container is not running</p>
        {:else if topErr}
          <p class="muted">{topErr}</p>
        {:else}
          <table>
            <thead><tr>{#each titles as t}<th>{t}</th>{/each}</tr></thead>
            <tbody>
              {#each procs as row}
                <tr>{#each row as cell}<td>{cell}</td>{/each}</tr>
              {/each}
              {#if procs.length === 0}
                <tr><td>collecting…</td></tr>
              {/if}
            </tbody>
          </table>
        {/if}
      </div>
    </section>

    <section class="pane">
      <h2>Terminal</h2>
      <div class="pane-body">
        {#if container.state === 'running'}
          <TermView wsPath={`/ws/docker/terminal?id=${container.id}`} title={container.name} subtitle="sh" />
        {:else}
          <p class="muted">start the container to open a shell</p>
        {/if}
      </div>
    </section>
  </div>
</div>

<style>
  .detail { display: flex; flex-direction: column; gap: 1rem; height: var(--page-h); }

  @media (max-width: 640px) {
    .cards { grid-template-columns: repeat(2, 1fr) !important; }
    .two-col { grid-template-columns: 1fr !important; }
  }

  header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; }
  .left { display: flex; align-items: center; gap: 0.6rem; }
  .back {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: var(--surface); border: 1px solid var(--border); color: var(--text-2);
    font-family: inherit; font-size: 0.8rem; padding: 0.4rem 0.75rem; border-radius: 5px; cursor: pointer;
  }
  .back:hover { border-color: var(--border-3); color: var(--text); }
  .dot { width: 9px; height: 9px; border-radius: 50%; }
  .name { color: var(--text); font-size: 1.15rem; font-weight: 600; }
  .image { color: var(--blue); font-size: 0.85rem; }
  .actions { display: flex; gap: 0.5rem; }
  .actions button {
    display: inline-flex; align-items: center; gap: 0.35rem;
    background: var(--surface-2); border: 1px solid var(--border); color: var(--text-2);
    font-family: inherit; font-size: 0.78rem; padding: 0.4rem 0.75rem; border-radius: 5px; cursor: pointer;
  }
  .actions button:hover { border-color: var(--border-3); color: var(--text); }
  .actions .ok { color: var(--green); border-color: var(--green-bd); }
  .actions .danger:hover { color: var(--red); border-color: var(--red-bd); }

  .cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1rem; }
  .card { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 1rem 1.25rem; display: flex; flex-direction: column; gap: 0.4rem; }
  .label { color: var(--text-muted); font-size: 0.75rem; text-transform: uppercase; letter-spacing: 1px; }
  .val { color: var(--text); font-size: 1.4rem; font-weight: 600; }
  .val.small { font-size: 0.9rem; font-weight: 500; overflow-wrap: anywhere; }
  .bar { height: 4px; background: var(--surface-2); border-radius: 2px; overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--green); }

  .two-col { flex: 1; min-height: 0; display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
  .pane { display: flex; flex-direction: column; min-height: 0; min-width: 0; }
  .pane h2 { font-size: 0.95rem; color: var(--text-2); margin-bottom: 0.5rem; flex-shrink: 0; }
  .pane-body { flex: 1; min-height: 0; }
  .pane-body.scroll { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; overflow: auto; }
  .muted { color: var(--text-muted); padding: 1rem; font-size: 0.85rem; }

  table { width: 100%; border-collapse: collapse; font-size: 0.8rem; }
  th, td { padding: 0.5rem 0.8rem; text-align: left; border-bottom: 1px solid var(--surface-2); white-space: nowrap; }
  th { color: var(--text-muted); font-weight: normal; position: sticky; top: 0; background: var(--surface); }
  td { color: var(--text); }
</style>

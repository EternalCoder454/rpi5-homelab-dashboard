<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import LogView from '../components/LogView.svelte';

  interface Svc {
    name: string;
    description: string;
    active: string;
    sub: string;
    enabled: string;
  }

  let services: Svc[] = [];
  let err = '';
  let search = '';
  let filter = 'active';
  let timer: ReturnType<typeof setInterval>;
  let logsFor: string | null = null;

  const FILTERS = [
    { key: 'active', label: 'Active' },
    { key: 'failed', label: 'Failed' },
    { key: 'inactive', label: 'Inactive' },
    { key: 'enabled', label: 'On boot' },
    { key: 'all', label: 'All' },
  ];
  const inFilter = (s: Svc, f: string) =>
    f === 'all'
      ? true
      : f === 'active'
        ? s.active === 'active' || s.active === 'activating'
        : f === 'failed'
          ? s.active === 'failed'
          : f === 'inactive'
            ? s.active !== 'active' && s.active !== 'activating' && s.active !== 'failed'
            : f === 'enabled'
              ? s.enabled === 'enabled'
              : true;

  async function load() {
    try {
      const d = await (await fetch('/api/services')).json();
      services = Array.isArray(d) ? d : [];
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  async function action(name: string, act: string) {
    err = '';
    try {
      const r = await fetch('/api/services/action', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, action: act }),
      });
      if (!r.ok) throw new Error((await r.text()).trim());
      setTimeout(load, 400);
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  function stateColor(s: Svc): string {
    if (s.active === 'active') return '#a6e3a1';
    if (s.active === 'failed') return '#f38ba8';
    if (s.active === 'activating' || s.active === 'deactivating') return '#fab387';
    return '#6c7086';
  }

  $: counts = Object.fromEntries(FILTERS.map((f) => [f.key, services.filter((s) => inFilter(s, f.key)).length]));
  $: filtered = services
    .filter((s) => inFilter(s, filter))
    .filter((s) => !search || s.name.toLowerCase().includes(search.toLowerCase()) || s.description.toLowerCase().includes(search.toLowerCase()));

  onMount(() => {
    load();
    timer = setInterval(() => !document.hidden && load(), 5000);
  });
  onDestroy(() => clearInterval(timer));
</script>

<svelte:window on:keydown={(e) => e.key === 'Escape' && (logsFor = null)} />

<div class="services">
  <header class="bar">
    <h1>Services</h1>
    <div class="tools">
      <input class="search" placeholder="Filter…" bind:value={search} />
      <button class="ghost" on:click={load} title="Refresh"><Icon name="refresh" /></button>
    </div>
  </header>

  <div class="filterbar">
    {#each FILTERS as f}
      <button class="fchip" class:on={filter === f.key} on:click={() => (filter = f.key)}>{f.label} <i>{counts[f.key]}</i></button>
    {/each}
  </div>

  {#if err}<div class="err" role="alert">⚠ {err} <button on:click={() => (err = '')}>dismiss</button></div>{/if}

  <div class="list">
    <table>
      <thead>
        <tr><th>Service</th><th>State</th><th>Boot</th><th class="act">Actions</th></tr>
      </thead>
      <tbody>
        {#each filtered as s (s.name)}
          <tr>
            <td class="name">
              <span class="dot" style="background:{stateColor(s)}"></span>
              <div class="nwrap">
                <span class="unit">{s.name}</span>
                <span class="desc">{s.description}</span>
              </div>
            </td>
            <td class="state">{s.active}{s.sub && s.sub !== s.active ? ` · ${s.sub}` : ''}</td>
            <td class="boot">
              {#if s.enabled === 'enabled' || s.enabled === 'disabled'}
                <button class="badge" class:on={s.enabled === 'enabled'} on:click={() => action(s.name, s.enabled === 'enabled' ? 'disable' : 'enable')} title="Toggle start-on-boot">
                  {s.enabled}
                </button>
              {:else}
                <span class="badge static">{s.enabled || '—'}</span>
              {/if}
            </td>
            <td class="act">
              {#if s.active === 'active'}
                <button class="iconbtn" title="Restart" on:click={() => action(s.name, 'restart')}><Icon name="refresh" /></button>
                <button class="iconbtn" title="Stop" on:click={() => action(s.name, 'stop')}><Icon name="square" /></button>
              {:else}
                <button class="iconbtn ok" title="Start" on:click={() => action(s.name, 'start')}><Icon name="play" /></button>
              {/if}
              <button class="iconbtn" title="Logs" on:click={() => (logsFor = s.name)}><Icon name="file" /></button>
            </td>
          </tr>
        {/each}
        {#if filtered.length === 0}
          <tr><td colspan="4" class="empty">no matching services</td></tr>
        {/if}
      </tbody>
    </table>
  </div>
</div>

{#if logsFor}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="overlay" on:click|self={() => (logsFor = null)}>
    <div class="panel">
      <div class="panel-head">
        <span>Logs — {logsFor}</span>
        <button class="x" on:click={() => (logsFor = null)}><Icon name="x" /></button>
      </div>
      <div class="panel-body">
        {#key logsFor}
          <LogView wsPath={`/ws/host/logs?unit=${encodeURIComponent(logsFor)}`} title={logsFor} />
        {/key}
      </div>
    </div>
  </div>
{/if}

<style>
  .services { display: flex; flex-direction: column; gap: 1rem; height: var(--page-h); }
  .bar { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; }
  h1 { font-size: 1.3rem; font-weight: 600; }
  .tools { display: flex; align-items: center; gap: 0.75rem; }
  .search { background: var(--surface); border: 1px solid var(--border); border-radius: 5px; color: var(--text); font-family: inherit; font-size: 0.85rem; padding: 0.4rem 0.7rem; }
  .filterbar { display: flex; flex-wrap: wrap; gap: 0.4rem; }
  .fchip { display: inline-flex; align-items: center; gap: 0.35rem; background: var(--surface); border: 1px solid var(--border); color: var(--text-muted); font-family: inherit; font-size: 0.78rem; padding: 0.3rem 0.7rem; border-radius: 14px; cursor: pointer; }
  .fchip:hover { border-color: var(--border-3); color: var(--text); }
  .fchip.on { background: var(--surface-3); color: var(--text); border-color: var(--blue); }
  .fchip i { color: var(--text-dim); font-style: normal; }
  .ghost { background: var(--surface); border: 1px solid var(--border); color: var(--text-muted); padding: 0.4rem 0.55rem; border-radius: 5px; cursor: pointer; display: inline-flex; }
  .ghost:hover { border-color: var(--border-3); color: var(--text); }

  .err { background: var(--red-bg); border: 1px solid var(--red-bd); color: var(--red); padding: 0.6rem 0.9rem; border-radius: 5px; font-size: 0.85rem; }
  .err button { background: none; border: none; color: var(--text-muted); font-family: inherit; cursor: pointer; text-decoration: underline; font-size: 0.75rem; }

  .list { flex: 1; overflow-y: auto; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
  table { width: 100%; border-collapse: collapse; }
  th, td { padding: 0.6rem 1rem; text-align: left; border-bottom: 1px solid var(--surface-2); font-size: 0.85rem; vertical-align: middle; }
  th { color: var(--text-muted); font-weight: normal; position: sticky; top: 0; background: var(--surface); }
  th.act, td.act { text-align: right; width: 11rem; white-space: nowrap; }
  .name { display: flex; align-items: center; gap: 0.6rem; }
  .dot { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
  .nwrap { display: flex; flex-direction: column; min-width: 0; }
  .unit { color: var(--text); }
  .desc { color: var(--text-muted); font-size: 0.75rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 40ch; }
  .state { color: var(--text-2); font-size: 0.8rem; }
  .badge { font-size: 0.72rem; padding: 0.15rem 0.5rem; border-radius: 10px; border: 1px solid var(--border); background: none; color: var(--text-muted); cursor: pointer; font-family: inherit; }
  .badge.on { color: var(--green); border-color: var(--green-bd); }
  .badge.static { cursor: default; }

  .iconbtn { display: inline-flex; align-items: center; justify-content: center; padding: 0.3rem; margin-left: 0.15rem; border: none; background: none; color: var(--text-dim); border-radius: 4px; cursor: pointer; }
  .iconbtn:hover { background: var(--surface-2); color: var(--text); }
  .iconbtn.ok { color: var(--green); }
  .empty { color: var(--text-muted); text-align: center; padding: 2rem; }

  .overlay { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6); display: flex; align-items: center; justify-content: center; z-index: 50; padding: 2rem; }
  .panel { width: 900px; max-width: 100%; height: 70vh; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; display: flex; flex-direction: column; overflow: hidden; }
  .panel-head { display: flex; align-items: center; justify-content: space-between; padding: 0.6rem 1rem; border-bottom: 1px solid var(--surface-2); color: var(--text); font-size: 0.85rem; }
  .panel-head .x { background: none; border: none; color: var(--text-muted); cursor: pointer; display: inline-flex; }
  .panel-head .x:hover { color: var(--red); }
  .panel-body { flex: 1; padding: 0.75rem; overflow: hidden; }

  @media (max-width: 640px) {
    .list { overflow-x: auto; }
    .desc { display: none; }
    /* hide the start-on-boot column on phones */
    .list table th:nth-child(3), .list table td:nth-child(3) { display: none; }
    .tools .search { width: 7rem; }
    .overlay { padding: 0; }
    .panel { width: 100%; height: 100%; max-height: none; border-radius: 0; }
  }
</style>

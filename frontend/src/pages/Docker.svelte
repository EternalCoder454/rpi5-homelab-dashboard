<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import TermView from '../components/TermView.svelte';
  import LogView from '../components/LogView.svelte';
  import ContainerDetail from '../components/ContainerDetail.svelte';

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

  const MIN_MEM = 16;

  let available = true;
  let dockerError = '';
  let containers: C[] = [];
  let err = '';
  let timer: ReturnType<typeof setInterval>;

  // create form
  let showCreate = false;
  let creating = false;
  let pullMsg = '';
  let f = { image: '', name: '', memory: 256, ports: '', env: '' };

  // logs / terminal modal
  let modal: { type: 'logs' | 'terminal'; id: string; name: string } | null = null;

  // open container detail page
  let selected: C | null = null;

  // tabs
  let tab: 'containers' | 'images' | 'volumes' = 'containers';
  let images: any[] = [];
  let volumes: any[] = [];

  async function loadImages() {
    try {
      const d = await (await fetch('/api/docker/images')).json();
      images = d.available && Array.isArray(d.images) ? d.images : [];
    } catch {
      /* transient */
    }
  }
  async function loadVolumes() {
    try {
      const d = await (await fetch('/api/docker/volumes')).json();
      volumes = d.available && Array.isArray(d.volumes) ? d.volumes : [];
    } catch {
      /* transient */
    }
  }
  function setTab(t: 'containers' | 'images' | 'volumes') {
    tab = t;
    if (t === 'images') loadImages();
    if (t === 'volumes') loadVolumes();
  }
  async function removeImage(id: string) {
    if (!confirm('Remove this image?')) return;
    err = '';
    try {
      const r = await fetch('/api/docker/images/remove', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id }),
      });
      if (!r.ok) throw new Error((await r.text()).trim());
      loadImages();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }
  async function removeVolume(name: string) {
    if (!confirm('Remove this volume? Its data is deleted permanently.')) return;
    err = '';
    try {
      const r = await fetch('/api/docker/volumes/remove', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      });
      if (!r.ok) throw new Error((await r.text()).trim());
      loadVolumes();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  async function load() {
    try {
      const d = await (await fetch('/api/docker/containers')).json();
      available = !!d.available;
      dockerError = d.error || '';
      containers = Array.isArray(d.containers) ? d.containers : [];
      // Keep the open detail view's data fresh; close it if the container is gone.
      if (selected) selected = containers.find((c) => c.id === selected!.id) ?? null;
    } catch (e) {
      available = false;
      dockerError = String(e instanceof Error ? e.message : e);
    }
  }

  async function detailAction(act: string) {
    if (!selected) return;
    await action(selected, act);
    if (act === 'remove') selected = null;
  }

  async function action(c: C, act: string) {
    if (act === 'remove' && !confirm(`Remove container "${c.name}"? This deletes it and its anonymous volumes.`)) return;
    err = '';
    try {
      const r = await fetch('/api/docker/action', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: c.id, action: act }),
      });
      if (!r.ok) throw new Error((await r.text()).trim());
      await load();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  async function submitCreate() {
    if (!f.image.trim()) {
      err = 'Image is required';
      return;
    }
    if (f.memory < MIN_MEM) {
      err = `Memory must be at least ${MIN_MEM} MB`;
      return;
    }
    creating = true;
    pullMsg = 'starting…';
    err = '';
    try {
      const ports = f.ports.split(/[\s,]+/).filter(Boolean);
      const env = f.env.split('\n').map((s) => s.trim()).filter(Boolean);
      const r = await fetch('/api/docker/create', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: f.name.trim(), image: f.image.trim(), memory_mb: f.memory, ports, env }),
      });
      if (!r.ok) throw new Error((await r.text()).trim());
      const { pull_id } = await r.json();
      pollPull(pull_id);
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
      creating = false;
      pullMsg = '';
    }
  }

  function pollPull(id: string) {
    const iv = setInterval(async () => {
      try {
        const s = await (await fetch('/api/docker/pull/status?id=' + encodeURIComponent(id))).json();
        pullMsg = s.message || s.status;
        if (s.status === 'done') {
          clearInterval(iv);
          creating = false;
          pullMsg = '';
          showCreate = false;
          f = { image: '', name: '', memory: 256, ports: '', env: '' };
          load();
        } else if (s.status === 'error' || s.status === 'unknown') {
          clearInterval(iv);
          creating = false;
          if (s.status === 'error') err = s.message || 'create failed';
          pullMsg = '';
        }
      } catch {
        /* keep polling */
      }
    }, 1500);
  }

  function stateColor(s: string): string {
    if (s === 'running') return '#a6e3a1';
    if (s === 'paused' || s === 'restarting') return '#fab387';
    if (s === 'dead') return '#f38ba8';
    return '#6c7086';
  }
  function fmtBytes(n: number): string {
    if (!n) return '0';
    if (n < 1048576) return (n / 1024).toFixed(0) + 'K';
    if (n < 1073741824) return (n / 1048576).toFixed(0) + 'M';
    return (n / 1073741824).toFixed(1) + 'G';
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      modal = null;
      if (!creating) showCreate = false;
    }
  }

  onMount(() => {
    load();
    timer = setInterval(load, 3000);
  });
  onDestroy(() => clearInterval(timer));
</script>

<svelte:window on:keydown={onKey} />

<div class="docker">
  {#if selected}
    <ContainerDetail
      container={selected}
      on:back={() => (selected = null)}
      on:action={(e) => detailAction(e.detail)}
    />
  {:else}
  <header class="bar">
    <h1>Docker</h1>
    {#if available}
      <button class="primary" on:click={() => (showCreate = true)}>
        <Icon name="plus" /> New Container
      </button>
    {/if}
  </header>

  {#if available}
    <div class="tabs">
      <button class:on={tab === 'containers'} on:click={() => setTab('containers')}>Containers</button>
      <button class:on={tab === 'images'} on:click={() => setTab('images')}>Images</button>
      <button class:on={tab === 'volumes'} on:click={() => setTab('volumes')}>Volumes</button>
    </div>
  {/if}

  {#if err}
    <div class="err" role="alert">⚠ {err} <button class="dismiss" on:click={() => (err = '')}>dismiss</button></div>
  {/if}

  {#if !available}
    <div class="unavailable">
      <Icon name="box" size={32} />
      <h2>Docker isn't reachable</h2>
      <p>The daemon isn't installed or the service user can't access the socket.</p>
      {#if dockerError}<code>{dockerError}</code>{/if}
      <p class="hint">Install it on the Pi, add your user to the <code>docker</code> group, and restart the dashboard.</p>
    </div>
  {:else if tab === 'containers'}
    <div class="list">
      <table>
        <thead>
          <tr><th>Name</th><th>Image</th><th>State</th><th class="num">CPU</th><th class="num">Memory</th><th>Ports</th><th class="act">Actions</th></tr>
        </thead>
        <tbody>
          {#each containers as c (c.id)}
            <tr>
              <td class="name">
                <button class="row-name" on:click={() => (selected = c)}>
                  <span class="dot" style="background:{stateColor(c.state)}"></span>{c.name}
                </button>
              </td>
              <td class="image">{c.image}</td>
              <td class="state">{c.status}</td>
              <td class="num">{c.state === 'running' ? c.cpu_percent.toFixed(1) + '%' : '—'}</td>
              <td class="num">{c.state === 'running' ? `${fmtBytes(c.mem_usage)} / ${fmtBytes(c.mem_limit)}` : '—'}</td>
              <td class="ports">{c.ports || '—'}</td>
              <td class="act">
                {#if c.state === 'running'}
                  <button class="iconbtn" title="Stop" on:click={() => action(c, 'stop')}><Icon name="square" /></button>
                  <button class="iconbtn" title="Restart" on:click={() => action(c, 'restart')}><Icon name="refresh" /></button>
                  <button class="iconbtn" title="Terminal" on:click={() => (modal = { type: 'terminal', id: c.id, name: c.name })}><Icon name="terminal" /></button>
                {:else}
                  <button class="iconbtn ok" title="Start" on:click={() => action(c, 'start')}><Icon name="play" /></button>
                {/if}
                <button class="iconbtn" title="Logs" on:click={() => (modal = { type: 'logs', id: c.id, name: c.name })}><Icon name="file" /></button>
                <button class="iconbtn danger-hover" title="Remove" on:click={() => action(c, 'remove')}><Icon name="trash" /></button>
              </td>
            </tr>
          {/each}
          {#if containers.length === 0}
            <tr><td colspan="7" class="empty">no containers — create one to get started</td></tr>
          {/if}
        </tbody>
      </table>
    </div>
  {:else if tab === 'images'}
    <div class="list">
      <table>
        <thead><tr><th>Repository</th><th>ID</th><th class="num">Size</th><th class="act">Actions</th></tr></thead>
        <tbody>
          {#each images as im (im.id)}
            <tr>
              <td class="image">{im.repo}</td>
              <td class="mono">{im.id}</td>
              <td class="num">{fmtBytes(im.size)}</td>
              <td class="act"><button class="iconbtn danger-hover" title="Remove" on:click={() => removeImage(im.id)}><Icon name="trash" /></button></td>
            </tr>
          {/each}
          {#if images.length === 0}<tr><td colspan="4" class="empty">no images</td></tr>{/if}
        </tbody>
      </table>
    </div>
  {:else if tab === 'volumes'}
    <div class="list">
      <table>
        <thead><tr><th>Name</th><th>Driver</th><th>Mountpoint</th><th class="act">Actions</th></tr></thead>
        <tbody>
          {#each volumes as v (v.name)}
            <tr>
              <td class="mono">{v.name}</td>
              <td class="state">{v.driver}</td>
              <td class="mono dim">{v.mountpoint}</td>
              <td class="act"><button class="iconbtn danger-hover" title="Remove" on:click={() => removeVolume(v.name)}><Icon name="trash" /></button></td>
            </tr>
          {/each}
          {#if volumes.length === 0}<tr><td colspan="4" class="empty">no volumes</td></tr>{/if}
        </tbody>
      </table>
    </div>
  {/if}
  {/if}
</div>

<!-- Create modal -->
{#if showCreate}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="overlay" on:click|self={() => !creating && (showCreate = false)}>
    <div class="form-panel">
      <h2>New Container</h2>
      <label>Image <span class="req">*</span>
        <input bind:value={f.image} placeholder="e.g. nginx:alpine" spellcheck="false" />
      </label>
      <label>Name <span class="opt">(optional)</span>
        <input bind:value={f.name} placeholder="auto-generated if blank" spellcheck="false" />
      </label>
      <label>Memory limit — {f.memory} MB <span class="opt">(min {MIN_MEM})</span>
        <input type="range" min={MIN_MEM} max="2048" step="16" bind:value={f.memory} />
      </label>
      <label>Ports <span class="opt">(host:container, space-separated)</span>
        <input bind:value={f.ports} placeholder="8080:80 443:443" spellcheck="false" />
      </label>
      <label>Environment <span class="opt">(KEY=VALUE per line)</span>
        <textarea bind:value={f.env} rows="3" placeholder="TZ=UTC" spellcheck="false"></textarea>
      </label>

      {#if pullMsg}<div class="pulling"><span class="spin"></span>{pullMsg}</div>{/if}

      <div class="form-actions">
        <button class="ghost" on:click={() => (showCreate = false)} disabled={creating}>Cancel</button>
        <button class="primary" on:click={submitCreate} disabled={creating}>{creating ? 'Working…' : 'Create & Start'}</button>
      </div>
    </div>
  </div>
{/if}

<!-- Logs / Terminal modal -->
{#if modal}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="overlay" on:click|self={() => (modal = null)}>
    <div class="panel">
      <div class="panel-head">
        <span>{modal.type === 'logs' ? 'Logs' : 'Terminal'} — {modal.name}</span>
        <button class="x" on:click={() => (modal = null)}><Icon name="x" /></button>
      </div>
      <div class="panel-body">
        {#key modal.id}
          {#if modal.type === 'logs'}
            <LogView wsPath={`/ws/docker/logs?id=${modal.id}`} title={modal.name} />
          {:else}
            <TermView wsPath={`/ws/docker/terminal?id=${modal.id}`} title={modal.name} subtitle="sh" />
          {/if}
        {/key}
      </div>
    </div>
  </div>
{/if}

<style>
  .docker { display: flex; flex-direction: column; gap: 1rem; height: var(--page-h); }
  .bar { display: flex; align-items: center; justify-content: space-between; }
  h1 { font-size: 1.3rem; font-weight: 600; }

  .tabs { display: flex; gap: 0.25rem; border-bottom: 1px solid var(--surface-2); }
  .tabs button { background: none; border: none; border-bottom: 2px solid transparent; color: var(--text-muted); font-family: inherit; font-size: 0.85rem; padding: 0.5rem 0.9rem; cursor: pointer; margin-bottom: -1px; }
  .tabs button:hover { color: var(--text-2); }
  .tabs button.on { color: var(--text); border-bottom-color: var(--blue); }
  .mono { font-family: 'JetBrains Mono', monospace; font-size: 0.8rem; color: var(--text-2); }
  .mono.dim { color: var(--text-muted); font-size: 0.75rem; overflow-wrap: anywhere; }

  .primary {
    display: inline-flex; align-items: center; gap: 0.4rem;
    background: var(--green-bg); border: 1px solid var(--green-bd); color: var(--green);
    font-family: inherit; font-size: 0.85rem; padding: 0.45rem 0.9rem; border-radius: 5px; cursor: pointer;
  }
  .primary:hover:not(:disabled) { background: var(--green-bg); }
  .primary:disabled { opacity: 0.6; cursor: default; }

  .err { background: var(--red-bg); border: 1px solid var(--red-bd); color: var(--red); padding: 0.6rem 0.9rem; border-radius: 5px; font-size: 0.85rem; }
  .dismiss { background: none; border: none; color: var(--text-muted); font-family: inherit; font-size: 0.75rem; cursor: pointer; text-decoration: underline; }

  .unavailable {
    margin: auto; text-align: center; color: var(--text-muted); display: flex; flex-direction: column; align-items: center; gap: 0.5rem;
  }
  .unavailable h2 { color: var(--text); font-size: 1.1rem; }
  .unavailable code { background: var(--surface); padding: 0.3rem 0.6rem; border-radius: 4px; font-size: 0.8rem; color: var(--red); max-width: 600px; overflow-wrap: anywhere; }
  .unavailable .hint { font-size: 0.85rem; }

  .list { flex: 1; overflow-y: auto; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
  table { width: 100%; border-collapse: collapse; }
  th, td { padding: 0.7rem 1rem; text-align: left; border-bottom: 1px solid var(--surface-2); font-size: 0.85rem; }
  th { color: var(--text-muted); font-weight: normal; position: sticky; top: 0; background: var(--surface); }
  th.num, td.num { text-align: right; width: 7rem; color: var(--text-2); }
  th.act, td.act { text-align: right; width: 13rem; white-space: nowrap; }
  .name { color: var(--text); }
  .row-name {
    display: inline-flex; align-items: center;
    background: none; border: none; color: var(--text); cursor: pointer;
    font-family: inherit; font-size: 0.85rem; padding: 0;
  }
  .row-name:hover { color: var(--blue); }
  .row-name .dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 0.5rem; }
  .image { color: var(--blue); }
  .state { color: var(--text-muted); font-size: 0.78rem; }
  .ports { color: var(--text-2); font-size: 0.78rem; }
  .empty { color: var(--text-muted); text-align: center; padding: 2rem; }

  .iconbtn {
    display: inline-flex; align-items: center; justify-content: center;
    padding: 0.32rem; margin-left: 0.15rem; border: none; background: none;
    color: var(--text-dim); border-radius: 4px; cursor: pointer;
  }
  .iconbtn:hover { background: var(--surface-2); color: var(--text); }
  .iconbtn.ok { color: var(--green); }
  .iconbtn.danger-hover:hover { color: var(--red); }

  /* overlays */
  .overlay {
    position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6);
    display: flex; align-items: center; justify-content: center; z-index: 50; padding: 2rem;
  }
  .form-panel {
    width: 460px; max-width: 100%; background: var(--surface); border: 1px solid var(--border);
    border-radius: 10px; padding: 1.5rem; display: flex; flex-direction: column; gap: 0.85rem;
  }
  .form-panel h2 { font-size: 1.05rem; color: var(--text); }
  .form-panel label { display: flex; flex-direction: column; gap: 0.35rem; font-size: 0.8rem; color: var(--text-2); }
  .req { color: var(--red); }
  .opt { color: var(--text-muted); }
  .form-panel input[type='text'],
  .form-panel input:not([type]),
  .form-panel textarea {
    background: var(--bg); border: 1px solid var(--border); border-radius: 5px; color: var(--text);
    font-family: inherit; font-size: 0.85rem; padding: 0.5rem 0.65rem; resize: vertical;
  }
  .form-panel input[type='range'] { accent-color: var(--green); }
  .form-actions { display: flex; justify-content: flex-end; gap: 0.6rem; margin-top: 0.3rem; }
  .ghost { background: var(--surface-2); border: 1px solid var(--border); color: var(--text-2); font-family: inherit; font-size: 0.85rem; padding: 0.45rem 0.9rem; border-radius: 5px; cursor: pointer; }
  .pulling { display: flex; align-items: center; gap: 0.5rem; color: var(--blue); font-size: 0.82rem; }
  .spin { width: 12px; height: 12px; border: 2px solid var(--border); border-top-color: var(--blue); border-radius: 50%; animation: spin 0.7s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }

  .panel {
    width: 900px; max-width: 100%; height: 70vh; background: var(--surface); border: 1px solid var(--border);
    border-radius: 10px; display: flex; flex-direction: column; overflow: hidden;
  }
  .panel-head { display: flex; align-items: center; justify-content: space-between; padding: 0.6rem 1rem; border-bottom: 1px solid var(--surface-2); color: var(--text); font-size: 0.85rem; }
  .panel-head .x { background: none; border: none; color: var(--text-muted); cursor: pointer; display: inline-flex; }
  .panel-head .x:hover { color: var(--red); }
  .panel-body { flex: 1; padding: 0.75rem; overflow: hidden; }

  @media (max-width: 640px) {
    .list { overflow-x: auto; }
    /* drop the lower-priority columns (image/ports for containers, etc.) */
    .list table th:nth-child(2), .list table td:nth-child(2),
    .list table th:nth-child(6), .list table td:nth-child(6) { display: none; }
    .overlay { padding: 0; }
    .panel, .form-panel { width: 100%; height: 100%; max-height: none; border-radius: 0; }
  }
</style>

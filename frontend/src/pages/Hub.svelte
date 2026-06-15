<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import Gauge from '../components/Gauge.svelte';

  let metrics: any = {};
  let containers: any[] = [];
  let links: any[] = [];
  let checks: any[] = [];
  let err = '';
  let sysTimer: ReturnType<typeof setInterval>;
  let hubTimer: ReturnType<typeof setInterval>;

  let showLinkForm = false;
  let linkF = { name: '', url: '', icon: '🔗' };
  let editingLink: string | null = null;
  let showCheckForm = false;
  let checkF = { name: '', target: '' };
  let editingCheck: string | null = null;

  async function loadSystem() {
    try {
      metrics = await (await fetch('/api/metrics')).json();
      const d = await (await fetch('/api/docker/containers')).json();
      containers = d.available && Array.isArray(d.containers) ? d.containers : [];
    } catch {
      /* transient */
    }
  }
  async function loadHub() {
    try {
      const d = await (await fetch('/api/hub')).json();
      links = d.links || [];
      checks = d.checks || [];
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }
  async function post(url: string, body: unknown) {
    const r = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error((await r.text()).trim());
    return r.json();
  }

  // The Add-link form doubles as the Edit-link form: when editingLink is set,
  // saveLink() PUTs to /update with the id instead of POSTing a new one.
  function toggleLinkForm() {
    if (showLinkForm) {
      resetLinkForm();
    } else {
      editingLink = null;
      linkF = { name: '', url: '', icon: '🔗' };
      showLinkForm = true;
    }
  }
  function startEditLink(l: any) {
    editingLink = l.id;
    linkF = { name: l.name, url: l.url, icon: l.icon || '🔗' };
    showLinkForm = true;
  }
  function resetLinkForm() {
    editingLink = null;
    linkF = { name: '', url: '', icon: '🔗' };
    showLinkForm = false;
  }
  async function saveLink() {
    if (!linkF.name.trim() || !linkF.url.trim()) return;
    let url = linkF.url.trim();
    if (!/^https?:\/\//.test(url)) url = 'http://' + url;
    const body: any = { name: linkF.name.trim(), url, icon: linkF.icon.trim() || '🔗' };
    if (editingLink) body.id = editingLink;
    try {
      await post(editingLink ? '/api/hub/link/update' : '/api/hub/link/add', body);
      resetLinkForm();
      loadHub();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  function toggleCheckForm() {
    if (showCheckForm) {
      resetCheckForm();
    } else {
      editingCheck = null;
      checkF = { name: '', target: '' };
      showCheckForm = true;
    }
  }
  function startEditCheck(c: any) {
    editingCheck = c.id;
    checkF = { name: c.name, target: c.target };
    showCheckForm = true;
  }
  function resetCheckForm() {
    editingCheck = null;
    checkF = { name: '', target: '' };
    showCheckForm = false;
  }
  async function saveCheck() {
    if (!checkF.name.trim() || !checkF.target.trim()) return;
    const body: any = { name: checkF.name.trim(), target: checkF.target.trim() };
    if (editingCheck) body.id = editingCheck;
    try {
      await post(editingCheck ? '/api/hub/check/update' : '/api/hub/check/add', body);
      resetCheckForm();
      loadHub();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }
  const rmLink = (id: string) => post('/api/hub/link/remove', { id }).then(loadHub);
  const rmCheck = (id: string) => post('/api/hub/check/remove', { id }).then(loadHub);

  $: runningContainers = containers.filter((c) => c.state === 'running');
  const fmtUptime = (s: number) => (s ? `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m` : '--');
  function fmtBytes(n: number): string {
    if (!n) return '0';
    if (n < 1048576) return (n / 1024).toFixed(0) + 'K';
    if (n < 1073741824) return (n / 1048576).toFixed(0) + 'M';
    return (n / 1073741824).toFixed(1) + 'G';
  }

  onMount(() => {
    loadSystem();
    loadHub();
    sysTimer = setInterval(() => !document.hidden && loadSystem(), 5000);
    hubTimer = setInterval(() => !document.hidden && loadHub(), 15000);
  });
  onDestroy(() => {
    clearInterval(sysTimer);
    clearInterval(hubTimer);
  });
</script>

<div class="hub">
  <h1>Home</h1>

  <div class="gauges">
    <Gauge label="CPU" value={metrics.cpu_percent || 0} />
    <Gauge label="RAM" value={metrics.mem_percent || 0} />
    <Gauge label="Disk" value={metrics.disk_percent || 0} />
    <Gauge label="Temp" value={metrics.cpu_temp_c || 0} max={85} unit="°C" display={metrics.cpu_temp_c?.toFixed(1) ?? '--'} />
  </div>
  <div class="sysline">
    <span>Uptime <b>{fmtUptime(metrics.uptime_seconds)}</b></span>
    <span>Load <b>{metrics.load_1 ?? '--'}</b></span>
    <span>CPU freq <b>{metrics.cpu_freq_mhz?.toFixed(0) ?? '--'} MHz</b></span>
    <span>Mem <b>{metrics.mem_used_mb ?? 0} / {metrics.mem_total_mb ?? 0} MB</b></span>
  </div>

  <section>
    <div class="sec-head"><h2>Docker</h2></div>
    <div class="dlist">
      {#each runningContainers as c (c.id)}
        <div class="drow">
          <span class="dot up"></span>
          <span class="d-name">{c.name}</span>
          <span class="d-img">{c.image}</span>
          <span class="d-stat">cpu <b>{c.cpu_percent.toFixed(1)}%</b></span>
          <span class="d-stat">mem <b>{fmtBytes(c.mem_usage)}/{fmtBytes(c.mem_limit)}</b></span>
        </div>
      {/each}
      {#if runningContainers.length === 0}
        <p class="muted">No containers running.</p>
      {/if}
    </div>
  </section>

  <section>
    <div class="sec-head">
      <h2>Services</h2>
      <button class="add" on:click={toggleLinkForm}>
        <Icon name={showLinkForm ? 'x' : 'plus'} size={14} /> {showLinkForm ? 'Close' : 'Add link'}
      </button>
    </div>
    {#if showLinkForm}
      <div class="addform">
        {#if editingLink}<span class="editbadge"><Icon name="pencil" size={12} /> Editing</span>{/if}
        <input class="ico" maxlength="2" bind:value={linkF.icon} title="icon" />
        <input placeholder="Name" bind:value={linkF.name} />
        <input placeholder="URL or host:port" bind:value={linkF.url} on:keydown={(e) => e.key === 'Enter' && saveLink()} />
        <button class="ok" on:click={saveLink}>{editingLink ? 'Save' : 'Add'}</button>
        {#if editingLink}<button class="cancel" on:click={resetLinkForm}>Cancel</button>{/if}
      </div>
    {/if}
    <div class="tiles">
      {#each links as l (l.id)}
        <a class="tile" href={l.url} target="_blank" rel="noopener noreferrer">
          <span class="t-icon">{l.icon || '🔗'}</span>
          <span class="t-name">{l.name}</span>
          <span class="t-ext"><Icon name="external-link" size={13} /></span>
          <span class="t-actions">
            <button class="t-act" title="Edit" on:click|preventDefault|stopPropagation={() => startEditLink(l)}><Icon name="pencil" size={12} /></button>
            <button class="t-act rm" title="Remove" on:click|preventDefault|stopPropagation={() => rmLink(l.id)}><Icon name="x" size={12} /></button>
          </span>
        </a>
      {/each}
      {#if links.length === 0 && !showLinkForm}<p class="muted">No links yet — add tiles for the services you run.</p>{/if}
    </div>
  </section>

  <section>
    <div class="sec-head">
      <h2>Health</h2>
      <button class="add" on:click={toggleCheckForm}>
        <Icon name={showCheckForm ? 'x' : 'plus'} size={14} /> {showCheckForm ? 'Close' : 'Add check'}
      </button>
    </div>
    {#if showCheckForm}
      <div class="addform">
        {#if editingCheck}<span class="editbadge"><Icon name="pencil" size={12} /> Editing</span>{/if}
        <input placeholder="Name" bind:value={checkF.name} />
        <input placeholder="URL or host:port to ping" bind:value={checkF.target} on:keydown={(e) => e.key === 'Enter' && saveCheck()} />
        <button class="ok" on:click={saveCheck}>{editingCheck ? 'Save' : 'Add'}</button>
        {#if editingCheck}<button class="cancel" on:click={resetCheckForm}>Cancel</button>{/if}
      </div>
    {/if}
    <div class="checks">
      {#each checks as c (c.id)}
        <div class="check">
          <span class="dot" class:up={c.up}></span>
          <span class="c-name">{c.name}</span>
          <span class="c-target">{c.target}</span>
          <span class="c-stat" class:down={!c.up}>
            {c.up ? (c.code ? c.code : 'up') : 'down'}{c.up && c.latency_ms ? ` · ${c.latency_ms}ms` : ''}
          </span>
          <button class="c-act" title="Edit" on:click={() => startEditCheck(c)}><Icon name="pencil" size={12} /></button>
          <button class="c-act rm" title="Remove" on:click={() => rmCheck(c.id)}><Icon name="x" size={12} /></button>
        </div>
      {/each}
      {#if checks.length === 0 && !showCheckForm}<p class="muted">No checks yet — add a URL or host:port to monitor.</p>{/if}
    </div>
  </section>

  {#if err}<div class="err" role="alert">⚠ {err} <button on:click={() => (err = '')}>dismiss</button></div>{/if}
</div>

<style>
  .hub { display: flex; flex-direction: column; gap: 1.5rem; }
  h1 { font-size: 1.3rem; font-weight: 600; }
  h2 { font-size: 1rem; color: var(--text-2); }

  .gauges { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.75rem; }
  .sysline { display: flex; flex-wrap: wrap; gap: 1.5rem; color: var(--text-muted); font-size: 0.82rem; margin-top: -0.5rem; }
  .sysline b { color: var(--text); font-weight: 600; }

  .sec-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 0.75rem; }
  .add { display: inline-flex; align-items: center; gap: 0.35rem; background: var(--surface); border: 1px solid var(--border); color: var(--text-2); font-family: inherit; font-size: 0.78rem; padding: 0.35rem 0.7rem; border-radius: 5px; cursor: pointer; }
  .add:hover { border-color: var(--border-3); color: var(--text); }

  .dlist { display: flex; flex-direction: column; gap: 0.4rem; }
  .drow { display: flex; align-items: center; gap: 0.9rem; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 0.55rem 0.9rem; font-size: 0.83rem; }
  .d-name { color: var(--text); min-width: 9rem; font-weight: 500; }
  .d-img { color: var(--blue); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .d-stat { color: var(--text-muted); white-space: nowrap; }
  .d-stat b { color: var(--text-2); }

  .addform { display: flex; gap: 0.5rem; margin-bottom: 0.75rem; }
  .addform input { background: var(--bg); border: 1px solid var(--border); border-radius: 5px; color: var(--text); font-family: inherit; font-size: 0.85rem; padding: 0.45rem 0.6rem; flex: 1; }
  .addform .ico { flex: 0 0 3rem; text-align: center; }
  .addform .ok { background: var(--green-bg); border: 1px solid var(--green-bd); color: var(--green); font-family: inherit; font-size: 0.85rem; padding: 0 1rem; border-radius: 5px; cursor: pointer; }
  .addform .cancel { background: var(--surface); border: 1px solid var(--border); color: var(--text-2); font-family: inherit; font-size: 0.85rem; padding: 0 0.9rem; border-radius: 5px; cursor: pointer; }
  .addform .cancel:hover { border-color: var(--border-3); color: var(--text); }
  .editbadge { display: inline-flex; align-items: center; gap: 0.3rem; align-self: center; color: var(--peach); font-size: 0.75rem; white-space: nowrap; }

  .tiles { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 0.75rem; }
  .tile { position: relative; display: flex; align-items: center; gap: 0.6rem; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 0.85rem 1rem; text-decoration: none; color: var(--text); transition: border-color 0.15s, background 0.15s; }
  .tile:hover { border-color: var(--border-3); background: var(--surface-2); }
  .t-icon { font-size: 1.3rem; }
  .t-name { flex: 1; font-size: 0.9rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .t-ext { color: var(--border-2); }
  .t-actions { position: absolute; top: 4px; right: 4px; display: flex; gap: 1px; opacity: 0; transition: opacity 0.15s; }
  .tile:hover .t-actions { opacity: 1; }
  .t-act { background: none; border: none; color: var(--border-3); cursor: pointer; display: inline-flex; padding: 2px; }
  .t-act:hover { color: var(--blue); }
  .t-act.rm:hover { color: var(--red); }

  .checks { display: flex; flex-direction: column; gap: 0.4rem; }
  .check { display: flex; align-items: center; gap: 0.75rem; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 0.6rem 0.9rem; font-size: 0.85rem; }
  .dot { width: 9px; height: 9px; border-radius: 50%; background: var(--red); box-shadow: 0 0 6px var(--red); flex-shrink: 0; }
  .dot.up { background: var(--green); box-shadow: 0 0 6px var(--green); }
  .c-name { color: var(--text); min-width: 8rem; }
  .c-target { color: var(--text-muted); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .c-stat { color: var(--green); font-size: 0.8rem; }
  .c-stat.down { color: var(--red); }
  .c-act { background: none; border: none; color: var(--border-3); cursor: pointer; display: inline-flex; padding: 2px; }
  .c-act:hover { color: var(--blue); }
  .c-act.rm:hover { color: var(--red); }

  .muted { color: var(--text-muted); font-size: 0.85rem; padding: 0.5rem 0; }
  .err { background: var(--red-bg); border: 1px solid var(--red-bd); color: var(--red); padding: 0.6rem 0.9rem; border-radius: 5px; font-size: 0.85rem; }
  .err button { background: none; border: none; color: var(--text-muted); font-family: inherit; cursor: pointer; text-decoration: underline; font-size: 0.75rem; }

  @media (max-width: 640px) {
    .gauges { grid-template-columns: repeat(2, 1fr); }
    .sysline { gap: 0.5rem 1.25rem; font-size: 0.78rem; }
    .tiles { grid-template-columns: repeat(2, 1fr); }
    .drow { flex-wrap: wrap; gap: 0.3rem 0.9rem; }
    .d-name { min-width: 0; }
    .d-img { flex-basis: 100%; order: 3; }
    .c-name { min-width: 0; }
    .c-target { display: none; }
  }
</style>

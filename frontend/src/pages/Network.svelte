<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import Icon from '../components/Icon.svelte';

  interface Device {
    mac: string;
    ip: string;
    vendor: string;
    hostname: string;
    kind: string;
    online: boolean;
    open_ports: number[];
    self: boolean;
    name?: string;
    hidden: boolean;
    tags: string[];
    user?: string;
  }

  const TAG_OPTIONS = ['Computer', 'Server', 'Router', 'Printer', 'Phone', 'NAS', 'AI', 'IoT', 'TV', 'Console', 'Camera', 'Other'];

  let devices: Device[] = [];
  let scannedAt = 0;
  let scanning = false;
  let err = '';
  let timer: ReturnType<typeof setInterval>;

  function readView(): 'list' | 'box' {
    try {
      return localStorage.getItem('net_view') === 'box' ? 'box' : 'list';
    } catch {
      return 'list';
    }
  }
  let view: 'list' | 'box' = readView();
  function setView(v: 'list' | 'box') {
    view = v;
    try {
      localStorage.setItem('net_view', v);
    } catch {
      /* ignore */
    }
  }
  let filterTag = '';
  let showHidden = false;

  let selected: Device | null = null;
  let dragIndex: number | null = null;

  // device detail / live stats
  let sshUser = '';
  let stats: Record<string, any> | null = null;
  let statsLoading = false;
  let statsErr = '';
  let showInstall = false;
  let pubkey = '';
  let connected = false;
  let pollTimer: ReturnType<typeof setInterval> | undefined;

  $: visible = devices.filter((d) => showHidden || !d.hidden);
  $: tagsPresent = [...new Set(visible.flatMap((d) => d.tags || []))].sort();
  $: shown = visible.filter((d) => !filterTag || (d.tags || []).includes(filterTag));

  const PORT_NAMES: Record<number, string> = {
    22: 'SSH', 139: 'NetBIOS', 445: 'SMB', 3389: 'RDP', 5900: 'VNC',
    80: 'HTTP', 443: 'HTTPS', 9100: 'Print', 631: 'IPP', 515: 'LPD',
  };
  const portLabel = (p: number) => PORT_NAMES[p] || String(p);

  async function load() {
    try {
      const d = await (await fetch('/api/network/devices')).json();
      devices = d.devices || [];
      scannedAt = d.scanned_at || 0;
      scanning = !!d.scanning;
      if (selected) selected = devices.find((x) => x.mac === selected!.mac) || selected;
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  async function rescan() {
    scanning = true;
    try {
      const d = await (await fetch('/api/network/rescan', { method: 'POST' })).json();
      devices = d.devices || [];
      scannedAt = d.scanned_at || 0;
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    } finally {
      scanning = false;
    }
  }

  async function post(url: string, body: unknown) {
    const r = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error((await r.text()).trim());
  }

  function saveOrder(macs: string[]) {
    post('/api/network/order', { order: macs }).catch((e) => (err = String(e)));
  }

  async function setMeta(d: Device, patch: Partial<Device>) {
    try {
      await post('/api/network/device', {
        mac: d.mac,
        name: patch.name ?? d.name ?? '',
        hidden: patch.hidden ?? d.hidden,
        tags: patch.tags ?? d.tags ?? [],
        user: patch.user ?? d.user ?? '',
      });
      load();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  function toggleTag(d: Device, tag: string) {
    const cur = d.tags || [];
    let next: string[];
    if (cur.includes(tag)) next = cur.filter((t) => t !== tag);
    else if (cur.length >= 3) return;
    else next = [...cur, tag];
    setMeta(d, { tags: next });
  }

  // --- drag to reorder ---
  function onDrop(target: number) {
    if (dragIndex === null || dragIndex === target) {
      dragIndex = null;
      return;
    }
    const arr = [...shown];
    const [moved] = arr.splice(dragIndex, 1);
    arr.splice(target, 0, moved);
    const rest = devices.filter((d) => !arr.includes(d));
    devices = [...arr, ...rest];
    saveOrder(devices.map((d) => d.mac));
    dragIndex = null;
  }

  function rename(d: Device) {
    const name = prompt('Label for this device', d.name || d.hostname || '');
    if (name !== null) setMeta(d, { name: name.trim() });
  }

  function stopPoll() {
    if (pollTimer) clearInterval(pollTimer);
    pollTimer = undefined;
  }

  function closeDetail() {
    stopPoll();
    connected = false;
    stats = null;
    statsErr = '';
    showInstall = false;
    selected = null;
  }

  function select(d: Device) {
    stopPoll();
    selected = d;
    stats = null;
    statsErr = '';
    showInstall = false;
    connected = false;
    sshUser = d.user || '';
    // The Pi (local), or any device with a saved username, connects on open.
    if (d.self || d.user) connect();
  }

  // connect "permanently": for remote hosts the username is saved (so it
  // reconnects on next open), and stats keep refreshing until Disconnect.
  async function connect() {
    if (!selected) return;
    if (!selected.self) {
      const u = sshUser.trim();
      if (!u) return;
      if (selected.user !== u) await setMeta(selected, { user: u });
    }
    connected = true;
    await fetchStats(false);
    stopPoll();
    pollTimer = setInterval(() => !document.hidden && fetchStats(true), 5000);
  }

  async function disconnect() {
    stopPoll();
    connected = false;
    stats = null;
    if (selected && !selected.self && selected.user) await setMeta(selected, { user: '' });
  }

  async function fetchStats(silent = false) {
    if (!selected) return;
    if (!silent) {
      statsLoading = true;
      stats = null;
    }
    statsErr = '';
    try {
      const q = new URLSearchParams({ ip: selected.ip });
      if (!selected.self) q.set('user', sshUser.trim());
      const d = await (await fetch('/api/network/stats?' + q)).json();
      if (d.ok) {
        stats = d.stats;
      } else {
        statsErr = d.error || 'Could not connect.';
        if (!silent) {
          connected = false;
          stopPoll();
        }
      }
    } catch (e) {
      statsErr = String(e instanceof Error ? e.message : e);
    } finally {
      statsLoading = false;
    }
  }

  async function loadPubkey() {
    if (!pubkey) {
      try {
        pubkey = (await (await fetch('/api/network/pubkey')).json()).pubkey;
      } catch (e) {
        statsErr = String(e instanceof Error ? e.message : e);
        return;
      }
    }
    showInstall = true;
  }

  const fmtUp = (s: number) =>
    s ? `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h ${Math.floor((s % 3600) / 60)}m` : '—';

  const ago = (ts: number) => {
    if (!ts) return '—';
    const s = Math.max(0, Math.floor(Date.now() / 1000 - ts));
    return s < 60 ? `${s}s ago` : `${Math.floor(s / 60)}m ago`;
  };
  const displayName = (d: Device) => d.name || d.hostname || d.vendor || d.ip;

  onMount(() => {
    load();
    timer = setInterval(() => !document.hidden && load(), 10000);
  });
  onDestroy(() => {
    clearInterval(timer);
    stopPoll();
  });
</script>

<div class="net">
  <header>
    <div class="left">
      <h1>Network</h1>
      <span class="sub">{shown.length} device{shown.length === 1 ? '' : 's'} · scanned {ago(scannedAt)}</span>
    </div>
    <div class="tools">
      <label class="chk"><input type="checkbox" bind:checked={showHidden} /> Hidden</label>
      <div class="seg">
        <button class:on={view === 'list'} on:click={() => setView('list')} title="List view"><Icon name="menu" size={16} /></button>
        <button class:on={view === 'box'} on:click={() => setView('box')} title="Box view"><Icon name="grid" size={16} /></button>
      </div>
      <button class="rescan" on:click={rescan} disabled={scanning}>
        <Icon name="refresh" size={15} /> {scanning ? 'Scanning…' : 'Rescan'}
      </button>
    </div>
  </header>

  <div class="filterbar">
    <button class="fchip" class:on={filterTag === ''} on:click={() => (filterTag = '')}>All</button>
    {#each tagsPresent as t}
      <button class="fchip" class:on={filterTag === t} on:click={() => (filterTag = t)}>{t}</button>
    {/each}
  </div>

  {#if err}<div class="err" role="alert">⚠ {err} <button on:click={() => (err = '')}>dismiss</button></div>{/if}

  {#if shown.length === 0}
    <p class="muted">{scanning ? 'Scanning the network…' : filterTag ? `No devices tagged “${filterTag}”.` : 'No devices found. Try Rescan.'}</p>
  {:else if view === 'list'}
    <div class="list">
      {#each shown as d, i (d.mac)}
        <div
          class="row"
          class:off={!d.online}
          class:sel={d.kind !== 'computer'}
          draggable="true"
          on:dragstart={() => (dragIndex = i)}
          on:dragover|preventDefault
          on:drop|preventDefault={() => onDrop(i)}
          on:click={() => select(d)}
          role="button"
          tabindex="0"
        >
          <span class="grip" title="Drag to reorder"><Icon name="grip" size={15} /></span>
          <span class="dot" class:up={d.online}></span>
          <span class="ic"><Icon name={d.kind === 'computer' ? 'monitor' : d.kind === 'router' ? 'network' : 'box'} size={16} /></span>
          <span class="nm">{displayName(d)}{#if d.self}<span class="meTag">this Pi</span>{/if}</span>
          <span class="ip">{d.ip}</span>
          <span class="vn">{d.vendor}</span>
          <span class="tags">{#each d.tags || [] as t}<span class="tag">{t}</span>{/each}</span>
        </div>
      {/each}
    </div>
  {:else}
    <div class="boxes">
      {#each shown as d, i (d.mac)}
        <div
          class="card"
          class:off={!d.online}
          draggable="true"
          on:dragstart={() => (dragIndex = i)}
          on:dragover|preventDefault
          on:drop|preventDefault={() => onDrop(i)}
          on:click={() => select(d)}
          role="button"
          tabindex="0"
        >
          <div class="card-top">
            <span class="ic"><Icon name={d.kind === 'computer' ? 'monitor' : d.kind === 'router' ? 'network' : 'box'} size={18} /></span>
            <span class="dot" class:up={d.online}></span>
          </div>
          <div class="card-name">{displayName(d)}{#if d.self}<span class="meTag">this Pi</span>{/if}</div>
          <div class="card-ip">{d.ip}</div>
          <div class="card-mac">{d.mac}</div>
          <div class="card-foot"><span class="tags">{#each d.tags || [] as t}<span class="tag">{t}</span>{/each}</span><span class="vn">{d.vendor}</span></div>
        </div>
      {/each}
    </div>
  {/if}
</div>

{#if selected}
  <!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
  <div class="overlay" on:click|self={closeDetail}>
    <div class="panel">
      <header class="phead">
        <div>
          <h2>{displayName(selected)}</h2>
          <span class="tags">{#each selected.tags || [] as t}<span class="tag">{t}</span>{/each}</span>
        </div>
        <button class="x" on:click={closeDetail} title="Close"><Icon name="x" size={18} /></button>
      </header>
      <div class="pbody">
        <div class="kv"><span>Status</span><b class="caps {selected.online ? 'okc' : 'badc'}">{selected.online ? 'online' : 'offline'}</b></div>
        <div class="kv"><span>IP address</span><b>{selected.ip}</b></div>
        <div class="kv"><span>MAC</span><b>{selected.mac}</b></div>
        <div class="kv"><span>Hostname</span><b>{selected.hostname || '—'}</b></div>
        <div class="kv"><span>Vendor</span><b>{selected.vendor || '—'}</b></div>
        <div class="kv">
          <span>Open ports</span>
          <b class="ports">
            {#if selected.open_ports.length}{#each selected.open_ports as p}<span class="port">{portLabel(p)}<i>{p}</i></span>{/each}{:else}—{/if}
          </b>
        </div>

        <div class="taggroup">
          <span class="tglabel">Tags · {(selected.tags || []).length}/3</span>
          <div class="tagchips">
            {#each TAG_OPTIONS as t}
              <button
                class="tagchip"
                class:on={(selected.tags || []).includes(t)}
                disabled={!(selected.tags || []).includes(t) && (selected.tags || []).length >= 3}
                on:click={() => toggleTag(selected, t)}
              >{t}</button>
            {/each}
          </div>
        </div>

        <div class="statsbox">
          <div class="slabel">
            <Icon name="cpu" size={14} /> Live statistics
            {#if connected}<span class="livedot" title="Live"></span>{/if}
          </div>

          {#if !connected && !selected.self}
            <div class="connrow">
              <input placeholder="ssh username" bind:value={sshUser} on:keydown={(e) => e.key === 'Enter' && connect()} />
              <button class="ok" on:click={connect} disabled={statsLoading || !sshUser.trim()}>{statsLoading ? '…' : 'Connect'}</button>
            </div>
          {/if}

          {#if statsLoading && !stats}
            <p class="dim">Connecting…</p>
          {/if}

          {#if statsErr}
            <div class="serr">{statsErr}</div>
            {#if !selected.self}<button class="link" on:click={loadPubkey}>Set up SSH access ▸</button>{/if}
          {/if}

          {#if showInstall}
            <div class="install">
              <p>On <b>{displayName(selected)}</b>, run this as the user you'll connect as (the machine needs an SSH server running):</p>
              <code>mkdir -p ~/.ssh &amp;&amp; echo "{pubkey}" &gt;&gt; ~/.ssh/authorized_keys</code>
            </div>
          {/if}

          {#if stats}
            <div class="sgrid">
              {#each [['CPU', stats.cpu_percent], ['RAM', stats.mem_percent], ['Disk', stats.disk_percent]] as [lbl, pct]}
                <div class="bstat">
                  <div class="brow"><span>{lbl}</span><b>{pct ?? '–'}%</b></div>
                  <div class="bar"><i style="width:{Math.min(100, Number(pct) || 0)}%"></i></div>
                </div>
              {/each}
            </div>
            <div class="kv"><span>CPU</span><b>{stats.cpu_model || '—'}</b></div>
            <div class="kv"><span>GPU</span><b>{stats.gpu || '—'}</b></div>
            <div class="kv"><span>RAM</span><b>{stats.mem_total_mb ? Math.round(stats.mem_total_mb / 1024) + ' GB' : '—'}</b></div>
            <div class="kv"><span>Disk</span><b>{stats.disk_model || (stats.disk_total_gb ? stats.disk_total_gb + ' GB' : '—')}</b></div>
            <div class="kv"><span>Memory</span><b>{stats.mem_used_mb ?? '–'} / {stats.mem_total_mb ?? '–'} MB</b></div>
            <div class="kv"><span>Disk used</span><b>{stats.disk_used_gb ?? '–'} / {stats.disk_total_gb ?? '–'} GB</b></div>
            <div class="kv"><span>Load</span><b>{stats.load ?? '–'}</b></div>
            <div class="kv"><span>Uptime</span><b>{fmtUp(stats.uptime_seconds)}</b></div>
            <div class="kv"><span>System</span><b>{stats.os ?? '–'} · {stats.cores ?? '?'} cores</b></div>
          {/if}
        </div>

        <div class="pactions">
          <button on:click={() => rename(selected)}><Icon name="pencil" size={14} /> Rename</button>
          <button class="hide" on:click={() => { setMeta(selected, { hidden: !selected.hidden }); closeDetail(); }}>
            <Icon name="eye" size={14} /> {selected.hidden ? 'Unhide' : 'Hide'}
          </button>
        </div>

        {#if connected && !selected.self}
          <button class="disconnect" on:click={disconnect}><Icon name="log-out" size={14} /> Disconnect</button>
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .net { display: flex; flex-direction: column; gap: 1.25rem; }
  header { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; }
  .left { display: flex; align-items: baseline; gap: 0.75rem; flex-wrap: wrap; }
  h1 { font-size: 1.3rem; font-weight: 600; }
  .sub { color: var(--text-muted); font-size: 0.82rem; }
  .tools { display: flex; align-items: center; gap: 0.75rem; }
  .chk { display: flex; align-items: center; gap: 0.35rem; color: var(--text-2); font-size: 0.82rem; cursor: pointer; }
  .seg { display: flex; border: 1px solid var(--border); border-radius: 6px; overflow: hidden; }
  .seg button { background: var(--surface); border: none; color: var(--text-muted); padding: 0.35rem 0.55rem; cursor: pointer; display: inline-flex; }
  .seg button.on { background: var(--surface-3); color: var(--text); }
  .rescan { display: inline-flex; align-items: center; gap: 0.4rem; background: var(--surface); border: 1px solid var(--border); color: var(--text-2); font-family: inherit; font-size: 0.82rem; padding: 0.4rem 0.8rem; border-radius: 6px; cursor: pointer; }
  .rescan:hover:not(:disabled) { border-color: var(--border-3); color: var(--text); }
  .rescan:disabled { opacity: 0.55; cursor: default; }

  .err { background: var(--red-bg); border: 1px solid var(--red-bd); color: var(--red); padding: 0.6rem 0.9rem; border-radius: 6px; font-size: 0.85rem; }
  .err button { background: none; border: none; color: var(--text-muted); font-family: inherit; cursor: pointer; text-decoration: underline; font-size: 0.75rem; }
  .muted { color: var(--text-muted); font-size: 0.9rem; padding: 1.5rem 0; }

  /* list view */
  .list { display: flex; flex-direction: column; gap: 0.4rem; }
  .row { display: flex; align-items: center; gap: 0.85rem; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 0.55rem 0.8rem; font-size: 0.85rem; cursor: pointer; }
  .row:hover { border-color: var(--border-3); }
  .row.off { opacity: 0.5; }
  .grip { color: var(--text-dim); cursor: grab; display: inline-flex; }
  .ic { color: var(--text-muted); display: inline-flex; }
  .nm { color: var(--text); font-weight: 500; min-width: 9rem; display: flex; align-items: center; gap: 0.5rem; }
  .ip { color: var(--text-2); width: 8.5rem; }
  .vn { color: var(--text-muted); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .meTag { font-size: 0.65rem; color: var(--blue); border: 1px solid var(--border-2); border-radius: 4px; padding: 0 0.35rem; }

  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--text-muted); flex-shrink: 0; }
  .dot.up { background: var(--green); box-shadow: 0 0 6px var(--green); }

  /* tags + filter chips */
  .filterbar { display: flex; flex-wrap: wrap; gap: 0.4rem; }
  .fchip { background: var(--surface); border: 1px solid var(--border); color: var(--text-muted); font-family: inherit; font-size: 0.78rem; padding: 0.3rem 0.7rem; border-radius: 14px; cursor: pointer; }
  .fchip:hover { border-color: var(--border-3); color: var(--text); }
  .fchip.on { background: var(--surface-3); color: var(--text); border-color: var(--blue); }
  .tags { display: flex; flex-wrap: wrap; gap: 0.3rem; }
  .tag { font-size: 0.66rem; text-transform: uppercase; letter-spacing: 0.4px; padding: 0.1rem 0.45rem; border-radius: 10px; border: 1px solid var(--border-2); color: var(--text-2); white-space: nowrap; }
  .taggroup { padding: 0.7rem 0 0.2rem; }
  .tglabel { color: var(--text-muted); font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.5px; }
  .tagchips { display: flex; flex-wrap: wrap; gap: 0.35rem; margin-top: 0.5rem; }
  .tagchip { background: var(--surface-2); border: 1px solid var(--border); color: var(--text-2); font-family: inherit; font-size: 0.75rem; padding: 0.25rem 0.6rem; border-radius: 12px; cursor: pointer; }
  .tagchip:hover:not(:disabled) { border-color: var(--border-3); color: var(--text); }
  .tagchip.on { background: var(--green-bg); border-color: var(--green-bd); color: var(--green); }
  .tagchip:disabled { opacity: 0.4; cursor: default; }

  /* box view */
  .boxes { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 0.85rem; }
  .card { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 0.85rem; cursor: pointer; display: flex; flex-direction: column; gap: 0.3rem; }
  .card:hover { border-color: var(--border-3); }
  .card.off { opacity: 0.5; }
  .card-top { display: flex; align-items: center; justify-content: space-between; }
  .card-name { color: var(--text); font-weight: 600; font-size: 0.9rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; display: flex; align-items: center; gap: 0.4rem; }
  .card-ip { color: var(--text-2); font-size: 0.85rem; }
  .card-mac { color: var(--text-muted); font-size: 0.72rem; }
  .card-foot { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; margin-top: 0.3rem; }
  .card-foot .vn { font-size: 0.72rem; text-align: right; }

  /* detail modal */
  .overlay { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.55); display: flex; align-items: center; justify-content: center; z-index: 60; padding: 2rem; }
  .panel { width: 460px; max-width: 100%; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; overflow: hidden; }
  .phead { display: flex; align-items: flex-start; justify-content: space-between; padding: 1rem 1.1rem; border-bottom: 1px solid var(--surface-2); }
  .phead h2 { font-size: 1.05rem; margin-bottom: 0.35rem; }
  .x { background: none; border: none; color: var(--text-muted); cursor: pointer; display: inline-flex; }
  .x:hover { color: var(--red); }
  .pbody { padding: 1rem 1.1rem; display: flex; flex-direction: column; gap: 0.1rem; }
  .kv { display: flex; align-items: baseline; justify-content: space-between; gap: 1rem; padding: 0.5rem 0; border-bottom: 1px solid var(--surface-2); }
  .kv span { color: var(--text-muted); font-size: 0.8rem; }
  .kv b { color: var(--text); font-size: 0.85rem; font-weight: 600; text-align: right; overflow-wrap: anywhere; }
  .okc { color: var(--green); }
  .badc { color: var(--text-muted); }
  .ports { display: flex; flex-wrap: wrap; gap: 0.35rem; justify-content: flex-end; }
  .port { font-size: 0.72rem; background: var(--surface-2); border: 1px solid var(--border); border-radius: 5px; padding: 0.1rem 0.4rem; color: var(--text-2); }
  .port i { color: var(--text-muted); font-style: normal; margin-left: 0.25rem; }

  .statsbox { margin: 0.85rem 0; display: flex; flex-direction: column; gap: 0.5rem; }
  .slabel { display: flex; align-items: center; gap: 0.4rem; color: var(--text-muted); font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.5px; }
  .connrow { display: flex; gap: 0.5rem; }
  .connrow input { flex: 1; background: var(--bg); border: 1px solid var(--border); border-radius: 5px; color: var(--text); font-family: inherit; font-size: 0.85rem; padding: 0.4rem 0.6rem; }
  .connrow .ok { background: var(--green-bg); border: 1px solid var(--green-bd); color: var(--green); font-family: inherit; font-size: 0.85rem; padding: 0 1rem; border-radius: 5px; cursor: pointer; }
  .connrow .ok:disabled { opacity: 0.5; cursor: default; }
  .dim { color: var(--text-muted); font-size: 0.82rem; }
  .serr { background: var(--red-bg); border: 1px solid var(--red-bd); color: var(--red); padding: 0.5rem 0.7rem; border-radius: 6px; font-size: 0.8rem; }
  .link { align-self: flex-start; background: none; border: none; color: var(--blue); font-family: inherit; font-size: 0.78rem; cursor: pointer; padding: 0; }
  .install { background: var(--surface-2); border: 1px solid var(--border); border-radius: 6px; padding: 0.7rem 0.8rem; font-size: 0.78rem; color: var(--text-2); }
  .install p { margin-bottom: 0.4rem; }
  .install code { display: block; background: var(--bg); border: 1px solid var(--border); border-radius: 4px; padding: 0.5rem 0.6rem; color: var(--green); font-size: 0.72rem; overflow-wrap: anywhere; user-select: all; }
  .sgrid { display: flex; flex-direction: column; gap: 0.5rem; }
  .bstat { display: flex; flex-direction: column; gap: 0.25rem; }
  .brow { display: flex; justify-content: space-between; font-size: 0.8rem; }
  .brow span { color: var(--text-muted); }
  .brow b { color: var(--text); }
  .bar { height: 6px; background: var(--surface-2); border-radius: 3px; overflow: hidden; }
  .bar i { display: block; height: 100%; background: var(--green); }

  .caps { font-variant: small-caps; text-transform: lowercase; letter-spacing: 0.5px; }
  .livedot { width: 7px; height: 7px; border-radius: 50%; background: var(--green); box-shadow: 0 0 6px var(--green); animation: pulse 1.6s ease-in-out infinite; }
  @keyframes pulse { 50% { opacity: 0.35; } }
  .disconnect { display: flex; align-items: center; justify-content: center; gap: 0.4rem; width: 100%; margin-top: 0.9rem; background: var(--red-bg); border: 1px solid var(--red-bd); color: var(--red); font-family: inherit; font-size: 0.85rem; padding: 0.5rem; border-radius: 6px; cursor: pointer; }
  .disconnect:hover { border-color: var(--red); }

  .pactions { display: flex; flex-wrap: wrap; gap: 0.5rem; }
  .pactions button { display: inline-flex; align-items: center; gap: 0.35rem; background: var(--surface-2); border: 1px solid var(--border); color: var(--text-2); font-family: inherit; font-size: 0.8rem; padding: 0.4rem 0.7rem; border-radius: 6px; cursor: pointer; }
  .pactions button:hover { border-color: var(--border-3); color: var(--text); }
  .pactions .hide:hover { color: var(--red); border-color: var(--red-bd); }

  @media (max-width: 640px) {
    .ip, .vn { display: none; }
    .nm { min-width: 0; flex: 1; }
    .overlay { padding: 0; }
    .panel { width: 100%; height: 100%; border-radius: 0; }
  }
</style>

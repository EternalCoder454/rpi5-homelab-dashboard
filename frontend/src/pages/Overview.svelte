<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import StatCard from '../components/StatCard.svelte';
  import NetGraph from '../components/NetGraph.svelte';

  let metrics: any = {};
  let netData: Array<{ rx: number; tx: number }> = [];
  let ws: WebSocket;

  interface Proc {
    pid: number;
    name: string;
    cpu_percent: number;
    mem_mb: number;
  }
  let procs: Proc[] = [];
  let procTimer: ReturnType<typeof setInterval> | undefined;

  // Static host identity (model, OS, kernel, hostname, IP, cores) — fetched once.
  let host: any = {};
  async function loadHost() {
    try {
      host = await (await fetch('/api/system/info')).json();
    } catch {
      /* transient */
    }
  }

  async function fetchProcs() {
    try {
      const d = await (await fetch('/api/processes')).json();
      procs = Array.isArray(d) ? d : [];
    } catch {
      /* transient fetch error — keep last list */
    }
  }

  onMount(() => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${proto}//${window.location.host}/ws/metrics`);
    ws.onmessage = (e) => {
      metrics = JSON.parse(e.data);
      netData = [...netData, { rx: metrics.net_rx_kbps, tx: metrics.net_tx_kbps }];
      if (netData.length > 60) netData = netData.slice(netData.length - 60);
    };
    loadHost();
    fetchProcs();
    procTimer = setInterval(() => !document.hidden && fetchProcs(), 5000);
  });

  onDestroy(() => {
    ws?.close();
    if (procTimer) clearInterval(procTimer);
  });

  const th = (v: number) => (v > 85 ? 'critical' : v > 70 ? 'warn' : 'normal');
  $: cpuThresh = th(metrics.cpu_percent || 0);
  $: memThresh = th(metrics.mem_percent || 0);
  $: diskThresh = th(metrics.disk_percent || 0);

  const fmtUptime = (s: number) => `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
</script>

<div class="overview">
  <header>
    <h1>System Overview</h1>
  </header>

  <div class="cards">
    <StatCard label="CPU" value={Math.round(metrics.cpu_percent) || 0} unit="%" threshold={cpuThresh} />
    <StatCard label="RAM" value={Math.round(metrics.mem_percent) || 0} unit="%" threshold={memThresh} />
    <StatCard label="Disk" value={Math.round(metrics.disk_percent) || 0} unit="%" threshold={diskThresh} />
    <StatCard
      label="Temp"
      value={metrics.cpu_temp_c?.toFixed(1) ?? '--'}
      unit="°C"
      threshold={Number(metrics.cpu_temp_c) > 70 ? 'warn' : 'normal'}
    />
  </div>

  <section class="host">
    <div class="host-title">
      <span class="hmodel">{host.model ?? 'Host'}</span>
      <span class="hsub">{host.hostname ?? '--'}{host.ip ? ` · ${host.ip}` : ''}</span>
    </div>
    <div class="host-grid">
      <div class="hitem"><span>OS</span><b>{host.os ?? '--'}</b></div>
      <div class="hitem"><span>Kernel</span><b>{host.kernel ?? '--'}</b></div>
      <div class="hitem"><span>CPU Cores</span><b>{host.cores ?? '--'}{host.arch ? ` · ${host.arch}` : ''}</b></div>
      <div class="hitem">
        <span>Updates</span>
        <b>{host.updates?.count ?? 0} available{host.updates?.security ? ` · ${host.updates.security} sec` : ''}</b>
      </div>
    </div>
  </section>

  <div class="graph-row">
    <div class="gauge-box">
      <span class="glabel">CPU FREQ</span>
      <span class="gval">{metrics.cpu_freq_mhz?.toFixed(0) ?? '--'}<small>MHz</small></span>
      <span class="glabel">LOAD · 1m</span>
      <span class="gval2">{metrics.load_1 ?? '--'}</span>
    </div>
    <NetGraph data={netData} />
  </div>

  <div class="detail-grid">
    <div class="dcard"><span>Memory</span><b>{metrics.mem_used_mb ?? 0} / {metrics.mem_total_mb ?? 0} MB</b></div>
    <div class="dcard"><span>Swap</span><b>{metrics.swap_used_mb ?? 0} / {metrics.swap_total_mb ?? 0} MB</b></div>
    <div class="dcard"><span>Disk{metrics.disk_name ? ` · ${metrics.disk_name}` : ''}</span><b>{(metrics.disk_used_gb ?? 0).toFixed(1)} / {(metrics.disk_total_gb ?? 0).toFixed(1)} GB</b></div>
    <div class="dcard"><span>Disk I/O · R/W</span><b>{(metrics.disk_read_mbps ?? 0).toFixed(1)} / {(metrics.disk_write_mbps ?? 0).toFixed(1)} MB/s</b></div>
    {#if metrics.disk_temp_c}<div class="dcard"><span>Disk Temp</span><b>{metrics.disk_temp_c.toFixed(1)} °C</b></div>{/if}
    <div class="dcard"><span>Net RX / TX</span><b>{(metrics.net_rx_kbps ?? 0).toFixed(1)} / {(metrics.net_tx_kbps ?? 0).toFixed(1)} kbps</b></div>
    <div class="dcard"><span>Load · 1/5/15</span><b>{metrics.load_1 ?? '--'} / {metrics.load_5 ?? '--'} / {metrics.load_15 ?? '--'}</b></div>
    <div class="dcard"><span>Uptime</span><b>{fmtUptime(metrics.uptime_seconds ?? 0)}</b></div>
    <div class="dcard"><span>Fan</span><b>{metrics.fan_percent != null && metrics.fan_percent >= 0 ? `${metrics.fan_percent}%` : (metrics.fan_state ?? 'N/A')}</b></div>
    <div class="dcard"><span>CPU Temp</span><b>{(metrics.cpu_temp_c ?? 0).toFixed(1)} °C</b></div>
    <div class="dcard">
      <span>Throttled</span>
      <b class="caps" style="color:{metrics.throttled && metrics.throttled !== '0x0' ? 'var(--peach)' : 'var(--green)'}">
        {!metrics.throttled ? '--' : metrics.throttled === '0x0' ? 'No' : metrics.throttled}
      </b>
    </div>
  </div>

  <section class="proc-section">
    <h2>Top Processes</h2>
    <table class="proc-table">
      <thead>
        <tr><th>PID</th><th>Name</th><th>CPU %</th><th>Mem MB</th></tr>
      </thead>
      <tbody>
        {#each procs as p}
          <tr>
            <td>{p.pid}</td>
            <td>{p.name}</td>
            <td style="color: {p.cpu_percent > 50 ? 'var(--red)' : 'var(--green)'}">{p.cpu_percent.toFixed(1)}</td>
            <td>{p.mem_mb.toFixed(1)}</td>
          </tr>
        {/each}
        {#if procs.length === 0}
          <tr><td colspan="4" class="empty">collecting…</td></tr>
        {/if}
      </tbody>
    </table>
  </section>
</div>

<style>
  .overview { display: flex; flex-direction: column; gap: 1.5rem; }
  header { display: flex; justify-content: space-between; align-items: center; }
  h1 { font-size: 1.3rem; font-weight: 600; color: var(--text); }

  .cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1rem; }

  .host {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 1rem 1.25rem;
    display: flex;
    flex-direction: column;
    gap: 0.9rem;
  }
  .host-title { display: flex; align-items: baseline; gap: 0.75rem; flex-wrap: wrap; }
  .hmodel { font-size: 1.05rem; font-weight: 600; color: var(--text); }
  .hsub { font-size: 0.82rem; color: var(--text-muted); }
  .host-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1rem; }
  .hitem { display: flex; flex-direction: column; gap: 0.3rem; min-width: 0; }
  .hitem span { color: var(--text-muted); font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.5px; }
  .hitem b { color: var(--text); font-size: 0.88rem; font-weight: 600; overflow-wrap: anywhere; }

  .graph-row { display: grid; grid-template-columns: 200px 1fr; gap: 1rem; align-items: stretch; }
  .gauge-box {
    background: var(--surface);
    border: 1px solid var(--border);
    padding: 1.25rem;
    border-radius: 8px;
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 0.25rem;
  }
  .glabel { color: var(--text-muted); font-size: 0.75rem; letter-spacing: 1px; }
  .gval { font-size: 1.9rem; color: var(--green); }
  .gval small { font-size: 0.5em; opacity: 0.7; margin-left: 4px; }
  .gval2 { font-size: 1.4rem; color: var(--blue); margin-bottom: 0.25rem; }

  .detail-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 1rem; }
  .dcard {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 1rem;
    display: flex;
    flex-direction: column;
    gap: 0.4rem;
  }
  .dcard span { color: var(--text-muted); font-size: 0.75rem; letter-spacing: 0.5px; }
  .dcard b { color: var(--text); font-size: 1rem; font-weight: 600; }
  .dcard b.caps { font-variant: small-caps; text-transform: lowercase; letter-spacing: 1px; }

  .proc-section h2 { font-size: 1rem; color: var(--text-2); margin-bottom: 0.75rem; }
  .proc-table { width: 100%; border-collapse: collapse; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; overflow: hidden; }
  .proc-table th,
  .proc-table td { padding: 0.8rem 1rem; text-align: left; border-bottom: 1px solid var(--surface-2); }
  .proc-table th { color: var(--text-muted); font-weight: normal; letter-spacing: 1px; }
  .empty { color: var(--text-muted); text-align: center; }

  @media (max-width: 640px) {
    .cards { grid-template-columns: repeat(2, 1fr); }
    .graph-row { grid-template-columns: 1fr; }
    .host-grid { grid-template-columns: repeat(2, 1fr); }
    /* basic info only on a phone — drop the dense grid + process table */
    .detail-grid,
    .proc-section { display: none; }
  }
</style>

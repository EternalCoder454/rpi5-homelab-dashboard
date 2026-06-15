<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import Icon from './Icon.svelte';
  import PiLogo from './PiLogo.svelte';

  let info: any = {};
  let apt: any = { status: 'idle', running: false };
  let err = '';
  let infoTimer: ReturnType<typeof setInterval>;
  let aptTimer: ReturnType<typeof setInterval> | undefined;
  let confirmPower: '' | 'reboot' | 'poweroff' = '';

  async function loadInfo() {
    try {
      info = await (await fetch('/api/system/info')).json();
    } catch {
      /* transient */
    }
  }
  async function loadApt() {
    try {
      apt = await (await fetch('/api/system/apt-status')).json();
      if (!apt.running && aptTimer) {
        clearInterval(aptTimer);
        aptTimer = undefined;
        loadInfo();
      }
    } catch {
      /* transient */
    }
  }
  function pollApt() {
    if (!aptTimer) aptTimer = setInterval(loadApt, 2000);
  }

  async function post(url: string) {
    const r = await fetch(url, { method: 'POST' });
    if (!r.ok) throw new Error((await r.text()).trim());
    return r.json();
  }
  async function refresh() {
    err = '';
    try {
      await post('/api/system/refresh');
      apt = { status: 'running', running: true, action: 'refresh' };
      pollApt();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }
  async function upgrade() {
    if (!confirm('Apply all available updates now? This runs apt full-upgrade.')) return;
    err = '';
    try {
      await post('/api/system/upgrade');
      apt = { status: 'running', running: true, action: 'upgrade' };
      pollApt();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }
  async function power(action: 'reboot' | 'poweroff') {
    err = '';
    try {
      await fetch('/api/system/power', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ action }),
      });
      confirmPower = '';
      apt = { status: 'idle', running: false };
      info = { ...info, _down: action };
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  const fmtUptime = (s: number) => (s ? `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m` : '--');
  $: updCount = info.updates?.count ?? 0;
  $: updSec = info.updates?.security ?? 0;

  onMount(() => {
    loadInfo();
    loadApt();
    infoTimer = setInterval(loadInfo, 10000);
  });
  onDestroy(() => {
    clearInterval(infoTimer);
    if (aptTimer) clearInterval(aptTimer);
  });
</script>

<div class="syspanel">
  {#if err}<div class="err" role="alert">⚠ {err} <button on:click={() => (err = '')}>dismiss</button></div>{/if}
  {#if info._down}
    <div class="downnote">The Pi is {info._down === 'reboot' ? 'rebooting' : 'shutting down'} — this dashboard will be unreachable shortly{info._down === 'reboot' ? ' and should return in ~30s' : ''}.</div>
  {/if}

  <div class="hero">
    <PiLogo size={78} />
    <div class="os">{info.os ?? '--'}</div>
    {#if info.model}<div class="model">{info.model}</div>{/if}
  </div>

  <div class="info">
    <div class="row"><span>Kernel</span><b>{info.kernel ?? '--'}</b></div>
    <div class="row"><span>Uptime</span><b>{fmtUptime(info.uptime_seconds)}</b></div>
    <div class="row">
      <span>Updates</span>
      <b>{updCount} available{updSec ? ` · ${updSec} security` : ''}</b>
    </div>
  </div>

  <div class="actions">
    <div class="agroup">
      <span class="alabel">Updates</span>
      <div class="abtns">
        <button class="ib" title="Check for updates" on:click={refresh} disabled={apt.running}><Icon name="refresh" size={17} /></button>
        <button class="ib primary" title="Apply updates" on:click={upgrade} disabled={apt.running || updCount === 0}><Icon name="download2" size={17} /></button>
      </div>
    </div>

    <div class="agroup">
      <span class="alabel">Power</span>
      <div class="abtns">
        {#if confirmPower === 'reboot'}
          <span class="cmsg">Reboot now?</span>
          <button class="ib ok" title="Confirm reboot" on:click={() => power('reboot')}><Icon name="check" size={17} /></button>
          <button class="ib" title="Cancel" on:click={() => (confirmPower = '')}><Icon name="x" size={17} /></button>
        {:else if confirmPower === 'poweroff'}
          <span class="cmsg">Shut down?</span>
          <button class="ib ok" title="Confirm shutdown" on:click={() => power('poweroff')}><Icon name="check" size={17} /></button>
          <button class="ib" title="Cancel" on:click={() => (confirmPower = '')}><Icon name="x" size={17} /></button>
        {:else}
          <button class="ib" title="Reboot" on:click={() => (confirmPower = 'reboot')}><Icon name="refresh" size={17} /></button>
          <button class="ib danger" title="Shut down" on:click={() => (confirmPower = 'poweroff')}><Icon name="power" size={17} /></button>
        {/if}
      </div>
    </div>
  </div>

  {#if apt.status === 'running'}
    <div class="apt running"><span class="spin"></span>{apt.action === 'upgrade' ? 'Upgrading…' : 'Checking…'} (can take a few minutes)</div>
  {:else if apt.status === 'done'}
    <div class="apt done">✓ {apt.action === 'upgrade' ? 'Upgrade complete.' : 'Package lists refreshed.'}</div>
  {:else if apt.status === 'error'}
    <div class="apt error">⚠ {apt.message}</div>
  {/if}
</div>

<style>
  .syspanel { display: flex; flex-direction: column; gap: 1.5rem; }
  .err { background: var(--red-bg); border: 1px solid var(--red-bd); color: var(--red); padding: 0.6rem 0.9rem; border-radius: 5px; font-size: 0.85rem; }
  .err button { background: none; border: none; color: var(--text-muted); font-family: inherit; cursor: pointer; text-decoration: underline; font-size: 0.75rem; }
  .downnote { background: var(--surface-2); border: 1px solid var(--border); color: var(--peach); padding: 0.7rem 1rem; border-radius: 6px; font-size: 0.85rem; }

  .hero { display: flex; flex-direction: column; align-items: center; gap: 0.5rem; padding: 0.5rem 0 0.25rem; }
  .os { font-size: 1.15rem; font-weight: 600; color: var(--text); }
  .model { font-size: 0.82rem; color: var(--text-muted); }

  .info { display: flex; flex-direction: column; background: var(--surface-2); border-radius: 8px; overflow: hidden; }
  .row { display: flex; align-items: center; justify-content: space-between; padding: 0.7rem 1rem; border-bottom: 1px solid var(--surface); }
  .row:last-child { border-bottom: none; }
  .row span { color: var(--text-muted); font-size: 0.8rem; }
  .row b { color: var(--text); font-size: 0.88rem; font-weight: 600; }

  .actions { display: flex; gap: 1rem; }
  .agroup { flex: 1; background: var(--surface-2); border-radius: 8px; padding: 0.85rem 1rem; display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
  .alabel { color: var(--text-muted); font-size: 0.72rem; text-transform: uppercase; letter-spacing: 1px; }
  .abtns { display: flex; align-items: center; gap: 0.5rem; }
  .cmsg { color: var(--text-2); font-size: 0.8rem; }

  .ib {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--text-2);
    border-radius: 7px;
    cursor: pointer;
  }
  .ib:hover:not(:disabled) { border-color: var(--border-3); color: var(--text); }
  .ib:disabled { opacity: 0.4; cursor: default; }
  .ib.primary { background: var(--green-bg); border-color: var(--green-bd); color: var(--green); }
  .ib.ok { background: var(--green-bg); border-color: var(--green-bd); color: var(--green); }
  .ib.danger { color: var(--red); }
  .ib.danger:hover:not(:disabled) { border-color: var(--red-bd); color: var(--red); }

  .apt { font-size: 0.83rem; display: flex; align-items: center; gap: 0.5rem; }
  .apt.running { color: var(--blue); }
  .apt.done { color: var(--green); }
  .apt.error { color: var(--red); overflow-wrap: anywhere; }
  .spin { width: 12px; height: 12px; border: 2px solid var(--border); border-top-color: var(--blue); border-radius: 50%; animation: spin 0.7s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }

  @media (max-width: 640px) {
    .actions { flex-direction: column; }
  }
</style>

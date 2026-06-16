<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import Icon from './Icon.svelte';
  import PiLogo from './PiLogo.svelte';

  let info: any = {};
  let err = '';
  let infoTimer: ReturnType<typeof setInterval>;
  let confirmPower: '' | 'reboot' | 'poweroff' = '';

  async function loadInfo() {
    try {
      info = await (await fetch('/api/system/info')).json();
    } catch {
      /* transient */
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
      info = { ...info, _down: action };
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  const fmtUptime = (s: number) => (s ? `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m` : '--');

  onMount(() => {
    loadInfo();
    infoTimer = setInterval(loadInfo, 10000);
  });
  onDestroy(() => clearInterval(infoTimer));
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
  </div>

  <div class="actions">
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
  .ib.ok { background: var(--green-bg); border-color: var(--green-bd); color: var(--green); }
  .ib.danger { color: var(--red); }
  .ib.danger:hover:not(:disabled) { border-color: var(--red-bd); color: var(--red); }

  @media (max-width: 640px) {
    .actions { flex-direction: column; }
  }
</style>

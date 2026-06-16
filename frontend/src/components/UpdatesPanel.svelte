<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import Icon from './Icon.svelte';

  interface Pkg {
    name: string;
    current: string;
    candidate: string;
    arch: string;
    security: boolean;
    summary: string;
  }

  let pkgs: Pkg[] = [];
  let security = 0;
  let loading = true;
  let err = '';
  let apt: any = { status: 'idle', running: false, action: '' };
  let aptTimer: ReturnType<typeof setInterval> | undefined;

  // While an apt op runs, apt.action is "refresh" | "upgrade" | "update <pkg>".
  $: busyPkg = apt.running && apt.action?.startsWith('update ') ? apt.action.slice(7) : '';
  $: busyAll = apt.running && apt.action === 'upgrade';

  async function loadUpdates() {
    try {
      const d = await (await fetch('/api/system/updates')).json();
      pkgs = d.packages || [];
      security = d.security || 0;
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    } finally {
      loading = false;
    }
  }

  async function loadApt() {
    try {
      apt = await (await fetch('/api/system/apt-status')).json();
      if (!apt.running && aptTimer) {
        clearInterval(aptTimer);
        aptTimer = undefined;
        loadUpdates(); // refresh the list once the op finishes
      }
    } catch {
      /* transient */
    }
  }
  function pollApt() {
    if (!aptTimer) aptTimer = setInterval(loadApt, 2000);
  }

  async function post(url: string, body?: any) {
    const r = await fetch(url, {
      method: 'POST',
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    if (!r.ok) throw new Error((await r.text()).trim());
    return r.json();
  }

  async function check() {
    err = '';
    try {
      await post('/api/system/refresh');
      apt = { status: 'running', running: true, action: 'refresh' };
      pollApt();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }
  async function updateAll() {
    if (!confirm(`Update all ${pkgs.length} package(s) now?`)) return;
    err = '';
    try {
      await post('/api/system/upgrade');
      apt = { status: 'running', running: true, action: 'upgrade' };
      pollApt();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }
  async function updateOne(name: string) {
    err = '';
    try {
      await post('/api/system/update-package', { name });
      apt = { status: 'running', running: true, action: 'update ' + name };
      pollApt();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  onMount(() => {
    loadUpdates();
    loadApt();
  });
  onDestroy(() => {
    if (aptTimer) clearInterval(aptTimer);
  });
</script>

<div class="updates">
  {#if err}<div class="err" role="alert">⚠ {err} <button on:click={() => (err = '')}>dismiss</button></div>{/if}

  <div class="bar">
    <div class="count">
      {#if loading}
        Checking…
      {:else}
        <b>{pkgs.length}</b> update{pkgs.length === 1 ? '' : 's'} available{security ? ` · ${security} security` : ''}
      {/if}
    </div>
    <div class="tools">
      <button class="btn" on:click={check} disabled={apt.running}>
        <Icon name="refresh" size={15} /> Check
      </button>
      <button class="btn primary" on:click={updateAll} disabled={apt.running || pkgs.length === 0}>
        {#if busyAll}<span class="spin"></span>{:else}<Icon name="download2" size={15} />{/if} Update all
      </button>
    </div>
  </div>

  {#if apt.status === 'running' && apt.action === 'refresh'}
    <div class="note running"><span class="spin"></span> Refreshing package lists…</div>
  {:else if apt.status === 'done'}
    <div class="note done">✓ Done.</div>
  {:else if apt.status === 'error'}
    <div class="note error">⚠ {apt.message}</div>
  {/if}

  {#if !loading && pkgs.length === 0}
    <div class="empty">
      <Icon name="check" size={20} />
      <p>Everything is up to date.</p>
      <span>Use Check to refresh the package lists.</span>
    </div>
  {:else}
    <div class="list">
      {#each pkgs as p (p.name)}
        <div class="pkg" class:busy={busyPkg === p.name}>
          <div class="meta">
            <div class="name">
              {p.name}
              {#if p.security}<span class="sec">security</span>{/if}
            </div>
            {#if p.summary}<div class="summary">{p.summary}</div>{/if}
            <div class="ver">
              <span class="old">{p.current || '—'}</span>
              <span class="arr">→</span>
              <span class="new">{p.candidate}</span>
              <span class="arch">{p.arch}</span>
            </div>
          </div>
          <button
            class="upd"
            title="Update {p.name}"
            on:click={() => updateOne(p.name)}
            disabled={apt.running}
          >
            {#if busyPkg === p.name}<span class="spin"></span> Updating…{:else}Update{/if}
          </button>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .updates {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .err {
    background: var(--red-bg);
    border: 1px solid var(--red-bd);
    color: var(--red);
    padding: 0.6rem 0.9rem;
    border-radius: 5px;
    font-size: 0.85rem;
  }
  .err button {
    background: none;
    border: none;
    color: var(--text-muted);
    font-family: inherit;
    cursor: pointer;
    text-decoration: underline;
    font-size: 0.75rem;
  }

  .bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
  }
  .count {
    color: var(--text-2);
    font-size: 0.88rem;
  }
  .count b {
    color: var(--text);
  }
  .tools {
    display: flex;
    gap: 0.5rem;
  }
  .btn {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    border: 1px solid var(--border-2);
    background: var(--surface-2);
    color: var(--text);
    border-radius: 8px;
    padding: 0.45rem 0.8rem;
    cursor: pointer;
    font-family: inherit;
    font-size: 0.85rem;
  }
  .btn:hover:not(:disabled) {
    background: var(--surface-3);
  }
  .btn:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .btn.primary {
    background: var(--green-bg);
    border-color: var(--green-bd);
    color: var(--green);
  }

  .note {
    font-size: 0.83rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .note.running {
    color: var(--blue);
  }
  .note.done {
    color: var(--green);
  }
  .note.error {
    color: var(--red);
    overflow-wrap: anywhere;
  }

  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.3rem;
    padding: 2rem 1rem;
    color: var(--text-muted);
    text-align: center;
  }
  .empty p {
    color: var(--text-2);
    font-size: 0.95rem;
  }
  .empty span {
    font-size: 0.78rem;
  }

  .list {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    max-height: 52vh;
    overflow-y: auto;
  }
  .pkg {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.7rem 0.9rem;
  }
  .pkg.busy {
    border-color: var(--blue);
  }
  .meta {
    min-width: 0;
  }
  .name {
    font-size: 0.9rem;
    font-weight: 600;
    color: var(--text);
    display: flex;
    align-items: center;
    gap: 0.5rem;
  }
  .sec {
    font-size: 0.66rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--red);
    background: var(--red-bg);
    border: 1px solid var(--red-bd);
    border-radius: 4px;
    padding: 0.05rem 0.35rem;
  }
  .summary {
    font-size: 0.8rem;
    color: var(--text-muted);
    margin: 0.2rem 0 0.35rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 28rem;
  }
  .ver {
    font-size: 0.78rem;
    display: flex;
    align-items: center;
    gap: 0.4rem;
    flex-wrap: wrap;
  }
  .old {
    color: var(--text-muted);
    text-decoration: line-through;
  }
  .arr {
    color: var(--text-dim);
  }
  .new {
    color: var(--green);
    font-weight: 600;
  }
  .arch {
    color: var(--text-dim);
    background: var(--surface);
    border-radius: 4px;
    padding: 0.02rem 0.3rem;
  }
  .upd {
    flex-shrink: 0;
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    border: 1px solid var(--mauve);
    background: var(--surface);
    color: var(--text);
    border-radius: 8px;
    padding: 0.45rem 0.85rem;
    cursor: pointer;
    font-family: inherit;
    font-size: 0.82rem;
  }
  .upd:hover:not(:disabled) {
    background: var(--surface-3);
  }
  .upd:disabled {
    opacity: 0.45;
    cursor: default;
  }

  .spin {
    width: 12px;
    height: 12px;
    border: 2px solid var(--border);
    border-top-color: var(--blue);
    border-radius: 50%;
    animation: spin 0.7s linear infinite;
    display: inline-block;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
</style>

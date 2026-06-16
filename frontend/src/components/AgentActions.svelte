<script lang="ts">
  import { onMount, tick } from 'svelte';
  import Icon from './Icon.svelte';

  // The action gateway UI: a menu of vetted actions plus the confirm + live-run
  // modal. The assistant can only PROPOSE actions; running one always goes
  // through this human-confirmed path. Exposed run() lets the chat's inline
  // action chips reuse the same flow.
  interface Action {
    id: string;
    label: string;
    description: string;
    category: string;
    destructive: boolean;
    param: string; // '' | 'percent'
    fixed?: string;
  }

  let actions: Action[] = [];
  let menuOpen = false;

  // confirm/run modal state
  let current: Action | null = null;
  let percent = 50;
  let phase: 'confirm' | 'running' | 'done' = 'confirm';
  let output = '';
  let exitCode: number | null = null;
  let runErr = '';
  let ws: WebSocket | null = null;
  let outEl: HTMLPreElement;

  const CATS: Record<string, string> = {
    maintenance: 'Maintenance',
    hardware: 'Hardware',
    docker: 'Docker',
    system: 'System',
  };
  $: grouped = Object.keys(CATS)
    .map((c) => ({ cat: c, label: CATS[c], items: actions.filter((a) => a.category === c) }))
    .filter((g) => g.items.length);

  onMount(async () => {
    try {
      actions = await (await fetch('/api/agent/actions')).json();
    } catch {
      actions = [];
    }
  });

  // Public: open the confirm modal for an action id (used by chat chips too).
  export function run(id: string, presetParam?: string) {
    const a = actions.find((x) => x.id === id);
    if (!a) return;
    menuOpen = false;
    current = a;
    phase = 'confirm';
    output = '';
    exitCode = null;
    runErr = '';
    if (a.param === 'percent') {
      const n = parseInt(presetParam ?? '', 10);
      percent = Number.isFinite(n) ? Math.min(100, Math.max(0, n)) : 50;
    }
  }
  export function hasAction(id: string): boolean {
    return actions.some((a) => a.id === id);
  }
  export function label(id: string): string {
    return actions.find((a) => a.id === id)?.label ?? id;
  }

  function close() {
    ws?.close();
    ws = null;
    current = null;
  }

  function start() {
    if (!current) return;
    phase = 'running';
    output = '';
    exitCode = null;
    runErr = '';
    const param = current.param === 'percent' ? String(percent) : '';
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(`${proto}://${location.host}/ws/agent/run`);
    ws.onopen = () => ws?.send(JSON.stringify({ action: current!.id, param }));
    ws.onmessage = (e) => {
      let d: any;
      try {
        d = JSON.parse(e.data);
      } catch {
        return;
      }
      if (d.error) {
        runErr = d.error;
        phase = 'done';
        return;
      }
      if (d.line !== undefined) {
        output += d.line + '\n';
        tick().then(() => outEl && (outEl.scrollTop = outEl.scrollHeight));
      }
      if (d.done) {
        exitCode = d.exit;
        phase = 'done';
        ws?.close();
        ws = null;
      }
    };
    ws.onerror = () => {
      runErr = 'connection error';
      phase = 'done';
    };
  }
</script>

<div class="wrap">
  <button class="iconbtn" title="Actions" on:click={() => (menuOpen = !menuOpen)}>
    <Icon name="wrench" size={18} />
  </button>
  {#if menuOpen}
    <div class="menu">
      {#each grouped as g}
        <div class="cat">{g.label}</div>
        {#each g.items as a}
          <button class="item" class:danger={a.destructive} on:click={() => run(a.id)} title={a.description}>
            {a.label}
            {#if a.destructive}<Icon name="alert" size={13} />{/if}
          </button>
        {/each}
      {/each}
      {#if actions.length === 0}
        <div class="empty">No actions available.</div>
      {/if}
    </div>
  {/if}
</div>

{#if current}
  <div
    class="overlay"
    role="button"
    tabindex="-1"
    on:click|self={() => phase !== 'running' && close()}
    on:keydown={(e) => e.key === 'Escape' && phase !== 'running' && close()}
  >
    <div class="dialog">
      <header>
        <span><Icon name="wrench" size={15} /> {current.label}</span>
        {#if phase !== 'running'}
          <button class="x" on:click={close}><Icon name="x" size={18} /></button>
        {/if}
      </header>

      {#if phase === 'confirm'}
        <div class="body">
          <p class="desc">{current.description}</p>
          {#if current.param === 'percent'}
            <label class="pct">
              <span>Fan speed: <b>{percent}%</b></span>
              <input type="range" min="0" max="100" step="5" bind:value={percent} />
            </label>
          {/if}
          {#if current.destructive}
            <div class="warn"><Icon name="alert" size={14} /> This makes real changes to the Pi.</div>
          {/if}
        </div>
        <footer>
          <button class="ghost" on:click={close}>Cancel</button>
          <button class="primary" class:danger={current.destructive} on:click={start}>Run</button>
        </footer>
      {:else}
        <pre class="out" bind:this={outEl}>{output}{runErr ? '\n⚠ ' + runErr : ''}</pre>
        <footer>
          {#if phase === 'running'}
            <span class="status">Running…</span>
          {:else if runErr}
            <span class="status bad">Failed</span>
          {:else if exitCode === 0}
            <span class="status ok">Completed (exit 0)</span>
          {:else}
            <span class="status bad">Exited with code {exitCode}</span>
          {/if}
          <button class="ghost" disabled={phase === 'running'} on:click={close}>Close</button>
        </footer>
      {/if}
    </div>
  </div>
{/if}

<style>
  .wrap {
    position: relative;
  }
  .iconbtn {
    background: var(--surface-2);
    border: 1px solid var(--border);
    color: var(--text-2);
    border-radius: 8px;
    padding: 0.55rem;
    cursor: pointer;
    display: flex;
  }
  .iconbtn:hover {
    color: var(--text);
    background: var(--surface-3);
  }
  .menu {
    position: absolute;
    bottom: calc(100% + 0.4rem);
    left: 0;
    min-width: 15rem;
    background: var(--surface);
    border: 1px solid var(--border-2);
    border-radius: 8px;
    padding: 0.35rem;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
    z-index: 6;
    max-height: 60vh;
    overflow-y: auto;
  }
  .cat {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
    padding: 0.45rem 0.6rem 0.2rem;
  }
  .item {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    width: 100%;
    text-align: left;
    background: none;
    border: none;
    color: var(--text);
    padding: 0.45rem 0.6rem;
    border-radius: 6px;
    cursor: pointer;
    font-family: inherit;
    font-size: 0.88rem;
  }
  .item:hover {
    background: var(--surface-2);
  }
  .item.danger {
    color: var(--peach);
  }
  .empty {
    color: var(--text-muted);
    font-size: 0.82rem;
    padding: 0.5rem 0.6rem;
  }

  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 60;
    padding: 1rem;
  }
  .dialog {
    width: min(620px, 100%);
    max-height: 86vh;
    display: flex;
    flex-direction: column;
    background: var(--surface);
    border: 1px solid var(--border-2);
    border-radius: 12px;
    overflow: hidden;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.85rem 1.05rem;
    border-bottom: 1px solid var(--border);
    font-weight: 600;
  }
  header span {
    display: inline-flex;
    align-items: center;
    gap: 0.5rem;
  }
  .x {
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
  }
  .x:hover {
    color: var(--text);
  }
  .body {
    padding: 1rem 1.05rem;
  }
  .desc {
    color: var(--text-2);
    font-size: 0.9rem;
    line-height: 1.5;
  }
  .pct {
    display: block;
    margin-top: 0.9rem;
    font-size: 0.88rem;
    color: var(--text);
  }
  .pct input {
    width: 100%;
    margin-top: 0.4rem;
    accent-color: var(--mauve);
  }
  .warn {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    margin-top: 0.9rem;
    color: var(--peach);
    font-size: 0.82rem;
  }
  .out {
    margin: 0;
    padding: 0.9rem 1.05rem;
    background: var(--bg);
    color: var(--text);
    font-size: 0.8rem;
    line-height: 1.45;
    white-space: pre-wrap;
    word-break: break-word;
    overflow-y: auto;
    flex: 1;
    min-height: 8rem;
    max-height: 60vh;
  }
  footer {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 0.6rem;
    padding: 0.8rem 1.05rem;
    border-top: 1px solid var(--border);
  }
  .status {
    margin-right: auto;
    font-size: 0.84rem;
    color: var(--text-muted);
  }
  .status.ok {
    color: var(--green);
  }
  .status.bad {
    color: var(--red);
  }
  footer button {
    border-radius: 8px;
    padding: 0.5rem 1.1rem;
    cursor: pointer;
    font-family: inherit;
    font-size: 0.88rem;
    border: 1px solid var(--border-2);
  }
  .ghost {
    background: var(--surface-2);
    color: var(--text-2);
  }
  .ghost:hover:not(:disabled) {
    background: var(--surface-3);
  }
  .ghost:disabled {
    opacity: 0.5;
    cursor: default;
  }
  .primary {
    background: var(--mauve);
    color: var(--bg);
    border-color: var(--mauve);
    font-weight: 600;
  }
  .primary.danger {
    background: var(--peach);
    border-color: var(--peach);
  }
</style>

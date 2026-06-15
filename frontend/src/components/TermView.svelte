<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import type { Terminal as XTerm } from 'xterm';
  import type { FitAddon as XFit } from 'xterm-addon-fit';

  // Reusable xterm-over-WebSocket terminal. Used by the system Terminal page and
  // the per-container Docker terminal. The WS message protocol is fixed:
  // input as {type:'input',data}, resize as {rows,cols}; output is binary.
  export let wsPath: string;
  export let title = 'Terminal';
  export let subtitle = '';

  let container: HTMLElement;
  let term: XTerm, fit: XFit, ws: WebSocket;
  let destroyed = false;
  let connected = false;
  let resizeObs: ResizeObserver | undefined;

  // A fixed dark-gray terminal, independent of the app theme — so light themes
  // don't hide white output (e.g. the white parts of the Debian fastfetch logo).
  function termTheme() {
    return {
      background: '#262626',
      foreground: '#d4d4d4',
      cursor: '#d4d4d4',
      cursorAccent: '#262626',
      selectionBackground: '#454545',
    };
  }

  onMount(async () => {
    // Load xterm on demand so it stays out of the initial bundle — the default
    // Home page never needs it.
    const [{ Terminal }, { FitAddon }] = await Promise.all([
      import('xterm'),
      import('xterm-addon-fit'),
    ]);
    await import('xterm/css/xterm.css');
    if (destroyed) return;

    term = new Terminal({
      cursorBlink: true,
      fontFamily: "'Cousine', 'JetBrains Mono', ui-monospace, monospace",
      fontSize: 13,
      theme: termTheme(),
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    term.open(container);

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${proto}//${window.location.host}${wsPath}`);
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      connected = true;
      fit.fit();
      term.focus();
    };
    ws.onclose = () => (connected = false);
    ws.onerror = () => (connected = false);
    ws.onmessage = (e) => {
      if (e.data instanceof ArrayBuffer) term.write(new Uint8Array(e.data));
      else term.write(e.data);
    };

    term.onData((d) => ws.send(JSON.stringify({ type: 'input', data: d })));
    term.onResize(({ cols, rows }) => ws.send(JSON.stringify({ rows, cols })));

    resizeObs = new ResizeObserver(() => {
      try {
        fit.fit();
      } catch {
        /* not measurable yet */
      }
    });
    resizeObs.observe(container);
  });

  onDestroy(() => {
    destroyed = true;
    resizeObs?.disconnect();
    ws?.close();
    term?.dispose();
  });
</script>

<div class="term-wrap">
  <header class="term-header">
    <div class="left">
      <span class="dot" class:on={connected}></span>
      <span class="title">{title}</span>
      {#if subtitle}<span class="sep">·</span><span class="host">{subtitle}</span>{/if}
    </div>
    <span class="status">{connected ? 'connected' : 'disconnected'}</span>
  </header>
  <div bind:this={container} class="term-container"></div>
</div>

<style>
  .term-wrap {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: var(--bg);
    border: 1px solid var(--surface-2);
    border-radius: 6px;
    overflow: hidden;
  }
  .term-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.55rem 0.9rem;
    background: var(--surface);
    border-bottom: 1px solid var(--surface-2);
    font-size: 0.8rem;
    flex-shrink: 0;
  }
  .left { display: flex; align-items: center; gap: 0.5rem; }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--red);
    box-shadow: 0 0 6px var(--red);
    transition: background 0.2s, box-shadow 0.2s;
  }
  .dot.on { background: var(--green); box-shadow: 0 0 6px var(--green); }
  .title { color: var(--text); font-weight: 600; letter-spacing: 0.5px; }
  .sep { color: var(--border-2); }
  .host { color: var(--text-muted); }
  .status { color: var(--text-muted); font-size: 0.72rem; text-transform: uppercase; letter-spacing: 1px; }
  .term-container { flex: 1; padding: 0.5rem; overflow: hidden; background: #262626; }
</style>

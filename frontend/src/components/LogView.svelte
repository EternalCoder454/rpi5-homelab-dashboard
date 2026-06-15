<script lang="ts">
  import { onMount, onDestroy } from 'svelte';

  export let wsPath: string;
  export let title = 'Logs';

  let pre: HTMLPreElement;
  let ws: WebSocket;
  let connected = false;

  // Strip ANSI color codes for a clean text view.
  const ANSI = /\x1b\[[0-9;]*m/g;
  const MAX_NODES = 4000; // bound DOM growth on chatty containers

  function append(text: string) {
    if (!pre) return;
    const atBottom = pre.scrollHeight - pre.scrollTop - pre.clientHeight < 40;
    pre.appendChild(document.createTextNode(text.replace(ANSI, '')));
    while (pre.childNodes.length > MAX_NODES) pre.removeChild(pre.firstChild as Node);
    if (atBottom) pre.scrollTop = pre.scrollHeight;
  }

  onMount(() => {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${proto}//${window.location.host}${wsPath}`);
    ws.onopen = () => (connected = true);
    ws.onclose = () => (connected = false);
    ws.onerror = () => (connected = false);
    ws.onmessage = (e) => {
      if (typeof e.data === 'string') append(e.data);
    };
  });

  onDestroy(() => ws?.close());
</script>

<div class="log-wrap">
  <header>
    <div class="left">
      <span class="dot" class:on={connected}></span>
      <span class="title">{title}</span>
    </div>
    <span class="status">{connected ? 'streaming' : 'closed'}</span>
  </header>
  <pre bind:this={pre} class="log"></pre>
</div>

<style>
  .log-wrap {
    display: flex;
    flex-direction: column;
    height: 100%;
    background: var(--bg);
    border: 1px solid var(--surface-2);
    border-radius: 6px;
    overflow: hidden;
  }
  header {
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
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--red); box-shadow: 0 0 6px var(--red); }
  .dot.on { background: var(--green); box-shadow: 0 0 6px var(--green); }
  .title { color: var(--text); font-weight: 600; }
  .status { color: var(--text-muted); font-size: 0.72rem; text-transform: uppercase; letter-spacing: 1px; }
  .log {
    flex: 1;
    margin: 0;
    padding: 0.75rem 0.9rem;
    overflow: auto;
    font-family: 'JetBrains Mono', monospace;
    font-size: 12px;
    line-height: 1.5;
    color: var(--text);
    white-space: pre-wrap;
    word-break: break-word;
  }
</style>

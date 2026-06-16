<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import Icon from '../components/Icon.svelte';
  import LogoOrb from '../components/LogoOrb.svelte';
  import AgentActions from '../components/AgentActions.svelte';

  let actionsComp: AgentActions;

  // ===== settings (persisted server-side in assistant.json) =====
  interface QuickPrompt {
    name: string;
    prompt: string;
  }
  interface Settings {
    enabled: boolean;
    ollama_url: string;
    model: string;
    title: string;
    system_prompt: string;
    quick_prompts: QuickPrompt[];
  }
  let settings: Settings = {
    enabled: true,
    ollama_url: '',
    model: '',
    title: 'Atlas',
    system_prompt: '',
    quick_prompts: [],
  };
  let draft: Settings | null = null; // editable copy while the settings panel is open
  let showSettings = false;
  let saving = false;

  // ===== readiness probe =====
  let reachable = false;
  let ready = false;
  let probeErr = '';
  let installedModels: string[] = [];

  // ===== chat state =====
  type Msg = { role: 'user' | 'assistant'; content: string };
  let history: Msg[] = []; // sent to the model for context; the UI shows only the latest answer
  let answer = ''; // current (streaming) reply
  let busy = false;
  let input = '';
  let showQuick = false;
  let stats = { tokens: 0, tps: 0, total: 0 };
  let streamStart = 0;
  let streamTokens = 0;
  let scroller: HTMLDivElement;

  let ws: WebSocket | null = null;
  let probeTimer: ReturnType<typeof setInterval> | null = null;

  $: idle = !busy && !answer;

  // The assistant proposes actions with a [[action:ID]] (or [[action:fan-set|NN]])
  // directive. Strip those from the displayed text and surface them as Run chips
  // that go through the confirmed AgentActions flow — the model never executes.
  const ACTION_RE = /\[\[action:([a-z-]+)(?:\|(\d+))?\]\]/g;
  $: parsed = (() => {
    const chips: { id: string; param: string }[] = [];
    let m: RegExpExecArray | null;
    ACTION_RE.lastIndex = 0;
    while ((m = ACTION_RE.exec(answer))) {
      if (!chips.some((c) => c.id === m![1] && c.param === (m![2] || '')))
        chips.push({ id: m[1], param: m[2] || '' });
    }
    const text = answer
      .replace(ACTION_RE, '')
      .replace(/\[\[action[^\]]*$/, '') // hide a half-streamed directive
      .trimEnd();
    return { text, chips };
  })();

  // ----- caption beneath the title, mirroring the desktop app -----
  $: caption = (() => {
    if (!settings.enabled) return 'Assistant disabled — open settings to enable';
    if (busy && streamTokens > 0) {
      const el = (Date.now() - streamStart) / 1000;
      const rate = el > 0 ? streamTokens / el : 0;
      return `${settings.model}  ·  generating… ${streamTokens} tok, ${rate.toFixed(0)} tok/s`;
    }
    if (busy) return `${settings.model}  ·  thinking…`;
    if (!reachable) return 'Ollama not detected — see setup below';
    if (!ready) return 'Model not installed — see setup below';
    if (stats.tokens > 0)
      return `${settings.model}  ·  ${stats.tps.toFixed(1)} tok/s  ·  ${stats.tokens} tokens  ·  ${stats.total.toFixed(1)}s`;
    return `Model: ${settings.model}`;
  })();

  $: canSend = settings.enabled && ready && !busy;

  // ----- setup card text (shown when enabled but not ready) -----
  $: setup = (() => {
    if (!settings.enabled || ready) return null;
    if (!reachable)
      return {
        title: 'The assistant needs an Ollama server',
        body: `Couldn't reach Ollama at ${settings.ollama_url}. Ollama runs the model — usually on your main PC. Install it there, pull the model, and make sure it accepts LAN connections (OLLAMA_HOST=0.0.0.0). This clears itself once it's reachable.`,
        cmd: `curl -fsSL https://ollama.com/install.sh | sh\nOLLAMA_HOST=0.0.0.0 ollama serve\nollama pull ${settings.model}`,
      };
    return {
      title: 'Model not installed',
      body: `Ollama is reachable, but "${settings.model}" isn't installed on it yet. Pull it once (a few GB).`,
      cmd: `ollama pull ${settings.model}`,
    };
  })();

  // ===== lifecycle =====
  onMount(async () => {
    await loadConfig();
    await probe();
    probeTimer = setInterval(() => {
      if (!busy && !showSettings) probe();
    }, 5000);
  });
  onDestroy(() => {
    if (probeTimer) clearInterval(probeTimer);
    ws?.close();
  });

  async function loadConfig() {
    try {
      settings = await (await fetch('/api/assistant/config')).json();
    } catch {
      /* keep defaults */
    }
  }

  async function probe() {
    try {
      const d = await (await fetch('/api/assistant/probe')).json();
      reachable = !!d.reachable;
      ready = !!d.ready;
      installedModels = d.models || [];
      probeErr = d.error || '';
    } catch {
      reachable = false;
      ready = false;
      probeErr = 'probe failed';
    }
  }

  // ===== chat =====
  function send(text: string) {
    text = text.trim();
    if (!canSend || !text) return;
    showQuick = false;
    history = [...history, { role: 'user', content: text }];
    if (history.length > 8) history = history.slice(-8); // keep context small for a 3B model
    input = '';
    answer = '';
    busy = true;
    streamStart = Date.now();
    streamTokens = 0;
    stats = { tokens: 0, tps: 0, total: 0 };
    if (scroller) scroller.scrollTop = 0;

    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(`${proto}://${location.host}/ws/assistant/chat`);
    ws.onopen = () => ws?.send(JSON.stringify({ messages: history }));
    ws.onmessage = (e) => onFrame(e.data);
    ws.onerror = () => finish('⚠ connection error');
    ws.onclose = () => {
      if (busy) finish(''); // closed without a done frame
    };
  }

  function onFrame(raw: string) {
    let d: any;
    try {
      d = JSON.parse(raw);
    } catch {
      return;
    }
    if (d.error) {
      finish('⚠ ' + d.error);
      return;
    }
    if (d.token) {
      streamTokens++;
      answer += d.token;
      tick().then(() => {
        if (scroller) scroller.scrollTop = scroller.scrollHeight;
      });
    }
    if (d.done) {
      stats = { tokens: d.tokens || streamTokens, tps: d.tok_per_sec || 0, total: d.total_s || 0 };
      history = [...history, { role: 'assistant', content: answer }];
      if (history.length > 8) history = history.slice(-8);
      busy = false;
      ws?.close();
      ws = null;
    }
  }

  function finish(err: string) {
    if (err) answer = err;
    busy = false;
    ws?.close();
    ws = null;
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault();
      send(input);
    }
  }

  // ===== settings panel =====
  function openSettings() {
    draft = JSON.parse(JSON.stringify(settings));
    // ensure 3 quick-prompt rows to edit
    while (draft!.quick_prompts.length < 3) draft!.quick_prompts.push({ name: '', prompt: '' });
    showSettings = true;
  }
  async function saveSettings() {
    if (!draft) return;
    saving = true;
    draft.quick_prompts = draft.quick_prompts.filter((q) => q.name.trim() && q.prompt.trim());
    try {
      settings = await (
        await fetch('/api/assistant/config', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(draft),
        })
      ).json();
      showSettings = false;
      await probe();
    } finally {
      saving = false;
    }
  }

  let copied = false;
  function copyCmd(cmd: string) {
    navigator.clipboard?.writeText(cmd);
    copied = true;
    setTimeout(() => (copied = false), 1200);
  }

  // ===== minimal, safe Markdown -> HTML =====
  function md(src: string): string {
    const esc = (s: string) =>
      s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    const blocks: string[] = [];
    // fenced code first, stashed so inline rules don't touch it
    src = src.replace(/```([\s\S]*?)```/g, (_m, code) => {
      blocks.push(`<pre><code>${esc(code.replace(/^\n/, ''))}</code></pre>`);
      return ` ${blocks.length - 1} `;
    });
    const lines = src.split('\n');
    let html = '';
    let inList = false;
    const inline = (t: string) =>
      esc(t)
        .replace(/`([^`]+)`/g, '<code>$1</code>')
        .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
        .replace(/(^|[^*])\*([^*]+)\*/g, '$1<em>$2</em>');
    for (let line of lines) {
      const ph = line.match(/^ (\d+) $/);
      if (ph) {
        if (inList) {
          html += '</ul>';
          inList = false;
        }
        html += blocks[+ph[1]];
        continue;
      }
      const h = line.match(/^(#{1,3})\s+(.*)$/);
      const li = line.match(/^\s*[-*]\s+(.*)$/);
      const ol = line.match(/^\s*\d+\.\s+(.*)$/);
      if (li || ol) {
        if (!inList) {
          html += '<ul>';
          inList = true;
        }
        html += `<li>${inline((li || ol)![1])}</li>`;
      } else {
        if (inList) {
          html += '</ul>';
          inList = false;
        }
        if (h) html += `<h${h[1].length}>${inline(h[2])}</h${h[1].length}>`;
        else if (line.trim() === '') html += '';
        else html += `<p>${inline(line)}</p>`;
      }
    }
    if (inList) html += '</ul>';
    return html;
  }
</script>

<div class="assistant">
  <button class="gear" on:click={openSettings} title="Assistant settings">
    <Icon name="settings" size={18} />
  </button>

  <div class="scroll" bind:this={scroller}>
    <header class="head">
      <LogoOrb size={120} active={busy} />
      <h1>{settings.title || 'Assistant'}</h1>
      <p class="caption">{caption}</p>
    </header>

    {#if setup}
      <div class="setup">
        <div class="setup-title">{setup.title}</div>
        <div class="setup-body">{setup.body}</div>
        <pre class="setup-cmd">{setup.cmd}</pre>
        <div class="setup-actions">
          <button on:click={() => copyCmd(setup.cmd)}>{copied ? 'Copied' : 'Copy commands'}</button>
          <button on:click={probe}>Recheck</button>
        </div>
      </div>
    {/if}

    <div class="answer">
      {#if idle}
        <span class="hint">Ask about this Pi, run an action with the wrench button, or pick a quick prompt.</span>
      {:else}
        {@html md(parsed.text)}
        {#if !busy && parsed.chips.length}
          <div class="chips">
            {#each parsed.chips as c}
              {#if actionsComp?.hasAction(c.id)}
                <button class="chip" on:click={() => actionsComp.run(c.id, c.param)}>
                  <Icon name="wrench" size={13} /> Run: {actionsComp.label(c.id)}{c.param ? ` (${c.param}%)` : ''}
                </button>
              {/if}
            {/each}
          </div>
        {/if}
      {/if}
    </div>
  </div>

  <div class="inputbar">
    <AgentActions bind:this={actionsComp} />
    <div class="quickwrap">
      <button
        class="iconbtn"
        title="Quick prompts"
        disabled={!canSend}
        on:click={() => (showQuick = !showQuick)}
      >
        <Icon name="list" size={18} />
      </button>
      {#if showQuick}
        <div class="quickmenu">
          {#each settings.quick_prompts as q}
            <button on:click={() => send(q.prompt)} title={q.prompt}>{q.name}</button>
          {/each}
          {#if settings.quick_prompts.length === 0}
            <div class="quickempty">No quick prompts — add some in settings.</div>
          {/if}
        </div>
      {/if}
    </div>
    <input
      class="entry"
      bind:value={input}
      on:keydown={onKey}
      disabled={!canSend}
      placeholder="Ask about your CPU, memory, processes, services…"
    />
    <button class="send" on:click={() => send(input)} disabled={!canSend || !input.trim()}>
      <Icon name="send" size={16} /> Send
    </button>
  </div>
</div>

{#if showSettings && draft}
  <div
    class="overlay"
    role="button"
    tabindex="-1"
    on:click|self={() => (showSettings = false)}
    on:keydown={(e) => e.key === 'Escape' && (showSettings = false)}
  >
    <div class="panel">
      <header class="panel-head">
        <span><Icon name="sparkles" size={16} /> Assistant settings</span>
        <button class="x" on:click={() => (showSettings = false)}><Icon name="x" size={18} /></button>
      </header>
      <div class="panel-body">
        <label class="row toggle">
          <span>Enabled</span>
          <input type="checkbox" bind:checked={draft.enabled} />
        </label>

        <label class="field">
          <span>Ollama server URL</span>
          <input bind:value={draft.ollama_url} placeholder="http://192.168.1.10:11434" />
          <small>Where Ollama runs — usually your main PC. Start it with OLLAMA_HOST=0.0.0.0 so the Pi can reach it.</small>
        </label>

        <label class="field">
          <span>Model</span>
          <input bind:value={draft.model} placeholder="qwen2.5:3b" />
          {#if installedModels.length}
            <small>Installed on server: {installedModels.join(', ')}</small>
          {/if}
        </label>

        <label class="field">
          <span>Assistant name</span>
          <input bind:value={draft.title} placeholder="Atlas" />
        </label>

        <label class="field">
          <span>System prompt</span>
          <textarea rows="4" bind:value={draft.system_prompt}></textarea>
          <small>Live Pi state (CPU, memory, temp, processes, services, containers) is appended automatically.</small>
        </label>

        <div class="field">
          <span>Quick prompts</span>
          {#each draft.quick_prompts as q, i}
            <div class="qp">
              <input class="qp-name" bind:value={draft.quick_prompts[i].name} placeholder="Name" />
              <input class="qp-text" bind:value={draft.quick_prompts[i].prompt} placeholder="Prompt sent to the model" />
            </div>
          {/each}
        </div>
      </div>
      <footer class="panel-foot">
        <button class="ghost" on:click={() => (showSettings = false)}>Cancel</button>
        <button class="primary" on:click={saveSettings} disabled={saving}>
          {saving ? 'Saving…' : 'Save'}
        </button>
      </footer>
    </div>
  </div>
{/if}

<style>
  .assistant {
    position: relative;
    height: var(--page-h);
    display: flex;
    flex-direction: column;
  }
  .gear {
    position: absolute;
    top: 0;
    right: 0;
    background: none;
    border: none;
    color: var(--text-muted);
    cursor: pointer;
    padding: 0.4rem;
    border-radius: 6px;
    z-index: 2;
  }
  .gear:hover {
    color: var(--text);
    background: var(--surface);
  }

  .scroll {
    flex: 1;
    overflow-y: auto;
    padding: 0.5rem 1rem 1rem;
  }
  .head {
    display: flex;
    flex-direction: column;
    align-items: center;
    text-align: center;
    margin-top: 1.5rem;
  }
  .head h1 {
    font-size: 1.7rem;
    font-weight: 700;
    color: var(--text);
    margin-top: 0.4rem;
  }
  .caption {
    color: var(--text-muted);
    font-size: 0.85rem;
    margin-top: 0.3rem;
  }

  .answer {
    max-width: 46rem;
    margin: 1.75rem auto 0;
    color: var(--text);
    font-size: 0.95rem;
    line-height: 1.55;
    word-break: break-word;
  }
  .answer .hint {
    color: var(--text-muted);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 0.5rem;
    margin-top: 1rem;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    background: var(--surface-2);
    border: 1px solid var(--mauve);
    color: var(--text);
    border-radius: 999px;
    padding: 0.4rem 0.85rem;
    cursor: pointer;
    font-family: inherit;
    font-size: 0.85rem;
  }
  .chip:hover {
    background: var(--surface-3);
  }
  .answer :global(p) {
    margin: 0.5rem 0;
  }
  .answer :global(ul) {
    margin: 0.5rem 0;
    padding-left: 1.3rem;
  }
  .answer :global(li) {
    margin: 0.2rem 0;
  }
  .answer :global(h1),
  .answer :global(h2),
  .answer :global(h3) {
    margin: 0.8rem 0 0.4rem;
    line-height: 1.3;
  }
  .answer :global(code) {
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0.05rem 0.3rem;
    font-size: 0.85em;
  }
  .answer :global(pre) {
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.7rem 0.9rem;
    overflow-x: auto;
    margin: 0.6rem 0;
  }
  .answer :global(pre code) {
    background: none;
    border: none;
    padding: 0;
  }

  /* ===== setup card ===== */
  .setup {
    max-width: 42rem;
    margin: 1.5rem auto 0;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 1rem 1.1rem;
  }
  .setup-title {
    font-weight: 600;
    color: var(--text);
  }
  .setup-body {
    color: var(--text-2);
    font-size: 0.85rem;
    margin: 0.4rem 0 0.7rem;
    line-height: 1.5;
  }
  .setup-cmd {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 0.6rem 0.8rem;
    font-size: 0.82rem;
    color: var(--green);
    white-space: pre-wrap;
    user-select: all;
  }
  .setup-actions {
    display: flex;
    gap: 0.5rem;
    margin-top: 0.7rem;
  }
  .setup-actions button {
    background: var(--surface-2);
    border: 1px solid var(--border-2);
    color: var(--text);
    border-radius: 6px;
    padding: 0.35rem 0.7rem;
    cursor: pointer;
    font-family: inherit;
    font-size: 0.82rem;
  }
  .setup-actions button:hover {
    background: var(--surface-3);
  }

  /* ===== input bar ===== */
  .inputbar {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.75rem 1rem;
    border-top: 1px solid var(--surface-2);
  }
  .quickwrap {
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
  .iconbtn:hover:not(:disabled) {
    color: var(--text);
    background: var(--surface-3);
  }
  .iconbtn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .quickmenu {
    position: absolute;
    bottom: calc(100% + 0.4rem);
    left: 0;
    min-width: 13rem;
    background: var(--surface);
    border: 1px solid var(--border-2);
    border-radius: 8px;
    padding: 0.3rem;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.35);
    z-index: 5;
  }
  .quickmenu button {
    display: block;
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
  .quickmenu button:hover {
    background: var(--surface-2);
  }
  .quickempty {
    color: var(--text-muted);
    font-size: 0.82rem;
    padding: 0.5rem 0.6rem;
  }
  .entry {
    flex: 1;
    background: var(--surface);
    border: 1px solid var(--border-2);
    color: var(--text);
    border-radius: 10px;
    padding: 0.65rem 0.9rem;
    font-family: inherit;
    font-size: 0.92rem;
    outline: none;
  }
  .entry:focus {
    border-color: var(--mauve);
  }
  .entry:disabled {
    opacity: 0.5;
  }
  .send {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    background: var(--mauve);
    border: none;
    color: var(--bg);
    font-weight: 600;
    border-radius: 10px;
    padding: 0.6rem 1rem;
    cursor: pointer;
    font-family: inherit;
    font-size: 0.9rem;
  }
  .send:disabled {
    opacity: 0.45;
    cursor: default;
  }

  /* ===== settings panel ===== */
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 50;
    padding: 1rem;
  }
  .panel {
    width: min(560px, 100%);
    max-height: 88vh;
    display: flex;
    flex-direction: column;
    background: var(--surface);
    border: 1px solid var(--border-2);
    border-radius: 12px;
    overflow: hidden;
  }
  .panel-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.9rem 1.1rem;
    border-bottom: 1px solid var(--border);
    font-weight: 600;
  }
  .panel-head span {
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
  .panel-body {
    padding: 1rem 1.1rem;
    overflow-y: auto;
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
  }
  .field > span {
    font-size: 0.82rem;
    color: var(--text-2);
    font-weight: 600;
  }
  .field small {
    color: var(--text-muted);
    font-size: 0.74rem;
    line-height: 1.4;
  }
  .field input,
  .field textarea {
    background: var(--bg);
    border: 1px solid var(--border-2);
    color: var(--text);
    border-radius: 8px;
    padding: 0.5rem 0.7rem;
    font-family: inherit;
    font-size: 0.88rem;
    outline: none;
    resize: vertical;
  }
  .field input:focus,
  .field textarea:focus {
    border-color: var(--mauve);
  }
  .row.toggle {
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 0.9rem;
    color: var(--text);
  }
  .qp {
    display: flex;
    gap: 0.4rem;
    margin-top: 0.4rem;
  }
  .qp-name {
    width: 35%;
  }
  .qp-text {
    flex: 1;
  }
  .qp input {
    background: var(--bg);
    border: 1px solid var(--border-2);
    color: var(--text);
    border-radius: 8px;
    padding: 0.45rem 0.6rem;
    font-family: inherit;
    font-size: 0.84rem;
    outline: none;
  }
  .panel-foot {
    display: flex;
    justify-content: flex-end;
    gap: 0.6rem;
    padding: 0.9rem 1.1rem;
    border-top: 1px solid var(--border);
  }
  .panel-foot button {
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
  .ghost:hover {
    background: var(--surface-3);
  }
  .primary {
    background: var(--mauve);
    color: var(--bg);
    border-color: var(--mauve);
    font-weight: 600;
  }
  .primary:disabled {
    opacity: 0.5;
    cursor: default;
  }

  @media (max-width: 640px) {
    .answer {
      font-size: 0.9rem;
    }
    .send span {
      display: none;
    }
  }
</style>

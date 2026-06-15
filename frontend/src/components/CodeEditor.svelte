<script lang="ts" context="module">
  // Minimal, dependency-free syntax highlighter. A single tokenizing pass keeps
  // it fast and avoids the "replace inside a span" bugs of naive regex chains.
  // It is intentionally approximate — enough to read config/code nicely without
  // shipping a multi-hundred-KB grammar library onto an SD card.
  const SLASH = new Set(['js', 'ts', 'go', 'c', 'cpp', 'h', 'java', 'rs', 'css', 'json']);
  const HASH = new Set(['py', 'sh', 'bash', 'yaml', 'yml', 'toml', 'ini', 'conf', 'cfg', 'env', 'service']);

  const KEYWORDS = new Set([
    'if','else','for','while','return','function','const','let','var','import','export',
    'from','class','new','async','await','try','catch','finally','switch','case','break',
    'continue','default','typeof','instanceof','this','null','undefined','true','false',
    'func','package','type','struct','interface','range','map','go','defer','chan','select',
    'def','elif','lambda','None','True','False','and','or','not','in','is','with','as','yield',
    'echo','then','fi','do','done','local','export','source','pub','fn','mut','use','impl',
  ]);

  function esc(s: string): string {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }

  export function langOf(filename: string): string {
    const ext = filename.split('.').pop()?.toLowerCase() ?? '';
    if (filename.toLowerCase() === 'makefile') return 'sh';
    if (filename.toLowerCase() === 'dockerfile') return 'sh';
    return ext;
  }

  export function highlight(code: string, lang: string): string {
    // Big files: skip highlighting to stay responsive while typing.
    if (code.length > 100_000) return esc(code);

    const commentParts: string[] = [];
    if (SLASH.has(lang) || (!HASH.has(lang) && lang !== 'md')) commentParts.push('\\/\\/[^\\n]*', '\\/\\*[\\s\\S]*?\\*\\/');
    if (HASH.has(lang)) commentParts.push('#[^\\n]*');
    const comment = commentParts.length ? `(${commentParts.join('|')})` : '(\\u0000)'; // never-match fallback

    const re = new RegExp(
      comment +
        '|("(?:\\\\.|[^"\\\\])*"|\'(?:\\\\.|[^\'\\\\])*\'|`(?:\\\\.|[^`\\\\])*`)' + // strings
        '|(\\b\\d[\\d_.xXa-fA-F]*\\b)' + // numbers
        '|([A-Za-z_$][A-Za-z0-9_$]*)' + // identifiers
        '|([\\s\\S])', // any single char
      'g'
    );

    let out = '';
    let m: RegExpExecArray | null;
    while ((m = re.exec(code)) !== null) {
      if (m[1] !== undefined) out += `<span class="c-com">${esc(m[1])}</span>`;
      else if (m[2] !== undefined) out += `<span class="c-str">${esc(m[2])}</span>`;
      else if (m[3] !== undefined) out += `<span class="c-num">${esc(m[3])}</span>`;
      else if (m[4] !== undefined)
        out += KEYWORDS.has(m[4]) ? `<span class="c-kw">${esc(m[4])}</span>` : esc(m[4]);
      else out += esc(m[5]);
    }
    return out;
  }
</script>

<script lang="ts">
  import { createEventDispatcher, tick } from 'svelte';

  export let filename: string;
  export let content: string;

  const dispatch = createEventDispatcher<{ save: string; close: void }>();

  let value = '';
  let baseline: string | null = null;
  let ta: HTMLTextAreaElement;
  let pre: HTMLPreElement;
  let gutter: HTMLDivElement;

  // Adopt the content prop as the new baseline whenever the parent changes it
  // (on open, and after a successful save). This resets the modified flag
  // without clobbering in-progress edits, since the prop only changes at those
  // two moments.
  $: if (content !== baseline) {
    value = content;
    baseline = content;
  }

  $: lang = langOf(filename);
  $: highlighted = highlight(value, lang) + '\n'; // trailing \n keeps last line visible
  $: lines = value.split('\n').length;
  $: modified = value !== baseline;

  // caret position for the status bar
  let caretLine = 1;
  let caretCol = 1;
  function updateCaret() {
    const upto = value.slice(0, ta.selectionStart);
    const nl = upto.lastIndexOf('\n');
    caretLine = (upto.match(/\n/g)?.length ?? 0) + 1;
    caretCol = ta.selectionStart - nl;
  }

  function syncScroll() {
    if (!pre || !gutter) return;
    pre.scrollTop = ta.scrollTop;
    pre.scrollLeft = ta.scrollLeft;
    gutter.scrollTop = ta.scrollTop;
  }

  function onKey(e: KeyboardEvent) {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      save();
      return;
    }
    if (e.key === 'Tab') {
      e.preventDefault();
      const s = ta.selectionStart,
        end = ta.selectionEnd;
      value = value.slice(0, s) + '  ' + value.slice(end);
      tick().then(() => {
        ta.selectionStart = ta.selectionEnd = s + 2;
      });
    }
  }

  function save() {
    if (!modified) return;
    dispatch('save', value);
  }
  function close() {
    dispatch('close');
  }
</script>

<div class="editor">
  <header>
    <div class="meta">
      <span class="name">{filename}</span>
      {#if modified}<span class="mod" title="Unsaved changes">●</span>{/if}
      <span class="lang">{lang || 'text'}</span>
    </div>
    <div class="actions">
      <button class="save" class:enabled={modified} on:click={save}>Save <kbd>⌃S</kbd></button>
      <button class="close" on:click={close}>Close</button>
    </div>
  </header>

  <div class="body">
    <div class="gutter" bind:this={gutter}>
      {#each Array(lines) as _, i}<div class="ln">{i + 1}</div>{/each}
    </div>
    <div class="code">
      <pre class="layer" bind:this={pre} aria-hidden="true"><code>{@html highlighted}</code></pre>
      <textarea
        bind:this={ta}
        bind:value
        on:scroll={syncScroll}
        on:keydown={onKey}
        on:keyup={updateCaret}
        on:click={updateCaret}
        spellcheck="false"
        autocomplete="off"
        autocapitalize="off"
      ></textarea>
    </div>
  </div>

  <footer>
    <span>Ln {caretLine}, Col {caretCol}</span>
    <span>{value.length} chars · {lines} lines</span>
  </footer>
</div>

<style>
  .editor {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    background: var(--bg);
    border: 1px solid var(--surface-2);
    border-radius: 6px;
    overflow: hidden;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.5rem 0.85rem;
    background: var(--surface);
    border-bottom: 1px solid var(--surface-2);
    flex-shrink: 0;
  }
  .meta { display: flex; align-items: center; gap: 0.6rem; font-size: 0.82rem; }
  .name { color: var(--text); font-weight: 600; }
  .mod { color: var(--peach); font-size: 0.7rem; }
  .lang {
    color: var(--text-muted);
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 1px;
    border: 1px solid var(--border);
    border-radius: 3px;
    padding: 1px 6px;
  }
  .actions { display: flex; gap: 0.5rem; }
  .actions button {
    font-family: inherit;
    font-size: 0.78rem;
    padding: 0.35rem 0.8rem;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: var(--surface-2);
    color: var(--text-muted);
    cursor: pointer;
  }
  .save.enabled { background: var(--green-bg); border-color: var(--green-bd); color: var(--green); }
  .close:hover { color: var(--red); border-color: var(--red); }
  kbd { font-size: 0.7em; opacity: 0.7; }

  .body { flex: 1; display: flex; overflow: hidden; position: relative; }

  /* Shared text metrics — gutter, pre and textarea MUST match exactly so the
     caret lines up with the highlighted text. */
  .gutter,
  .layer,
  textarea {
    font-family: 'JetBrains Mono', monospace;
    font-size: 13px;
    line-height: 1.5;
    tab-size: 2;
  }

  .gutter {
    flex-shrink: 0;
    width: 3.2rem;
    padding: 0.6rem 0.5rem 0.6rem 0;
    text-align: right;
    color: var(--border-2);
    background: var(--bg);
    border-right: 1px solid var(--surface-2);
    overflow: hidden;
    user-select: none;
  }
  .ln { height: calc(13px * 1.5); }

  .code { position: relative; flex: 1; overflow: hidden; }
  .layer,
  textarea {
    position: absolute;
    inset: 0;
    margin: 0;
    padding: 0.6rem 0.8rem;
    border: 0;
    white-space: pre;
    overflow: auto;
    box-sizing: border-box;
  }
  .layer {
    color: var(--text);
    pointer-events: none;
    background: transparent;
  }
  .layer code { font: inherit; }
  textarea {
    background: transparent;
    color: transparent;
    caret-color: var(--text);
    resize: none;
    outline: none;
  }
  textarea::selection { background: rgba(137, 180, 250, 0.3); }

  footer {
    display: flex;
    justify-content: space-between;
    padding: 0.35rem 0.85rem;
    background: var(--surface);
    border-top: 1px solid var(--surface-2);
    font-size: 0.72rem;
    color: var(--text-muted);
    flex-shrink: 0;
  }

  /* token colors */
  :global(.layer .c-com) { color: var(--text-muted); font-style: italic; }
  :global(.layer .c-str) { color: var(--green); }
  :global(.layer .c-num) { color: var(--peach); }
  :global(.layer .c-kw) { color: var(--mauve); }
</style>

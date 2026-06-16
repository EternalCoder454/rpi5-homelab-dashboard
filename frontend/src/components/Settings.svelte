<script lang="ts">
  import { createEventDispatcher, onDestroy } from 'svelte';
  import Icon from './Icon.svelte';
  import SystemPanel from './SystemPanel.svelte';
  import UpdatesPanel from './UpdatesPanel.svelte';

  export let theme: string;
  export let scale: number;
  const dispatch = createEventDispatcher<{ theme: string; scale: number; close: void }>();

  let section: 'system' | 'updates' | 'appearance' = 'system';

  // Draggable window: offset from the centered position, moved by the header.
  let dx = 0,
    dy = 0;
  let sx = 0,
    sy = 0,
    sdx = 0,
    sdy = 0,
    dragging = false;

  function onPointerDown(e: PointerEvent) {
    if ((e.target as HTMLElement).closest('.x')) return; // let the close button click
    dragging = true;
    sx = e.clientX;
    sy = e.clientY;
    sdx = dx;
    sdy = dy;
    window.addEventListener('pointermove', onPointerMove);
    window.addEventListener('pointerup', onPointerUp);
  }
  function onPointerMove(e: PointerEvent) {
    if (!dragging) return;
    dx = sdx + (e.clientX - sx);
    dy = sdy + (e.clientY - sy);
  }
  function onPointerUp() {
    dragging = false;
    window.removeEventListener('pointermove', onPointerMove);
    window.removeEventListener('pointerup', onPointerUp);
  }
  onDestroy(() => {
    window.removeEventListener('pointermove', onPointerMove);
    window.removeEventListener('pointerup', onPointerUp);
  });

  // [id, name, bg, fg, accent]
  const themes: Array<[string, string, string, string, string]> = [
    ['dark', 'Dark', '#18181b', '#cdd6f4', '#89b4fa'],
    ['mocha', 'Mocha', '#1e1e2e', '#cdd6f4', '#cba6f7'],
    ['tokyo', 'Tokyo Night', '#1a1b26', '#c0caf5', '#7aa2f7'],
    ['amoled', 'AMOLED', '#000000', '#cdd6f4', '#89b4fa'],
    ['dim', 'Dim', '#383b42', '#dfe1e8', '#8caaee'],
    ['light', 'Light', '#ffffff', '#4c4f69', '#1e66f5'],
    ['dawn', 'Rosé Pine Dawn', '#fffaf3', '#575279', '#d7827e'],
    ['solarized', 'Solarized Light', '#fdf6e3', '#586e75', '#268bd2'],
    ['everforest', 'Everforest', '#fffbef', '#5c6a72', '#8da101'],
  ];

  // [scale, label] — discrete steps, Minecraft GUI-scale style.
  const scales: Array<[number, string]> = [
    [0.85, 'Compact'],
    [1.0, 'Normal'],
    [1.15, 'Large'],
    [1.3, 'Larger'],
    [1.5, 'Huge'],
  ];
</script>

<svelte:window on:keydown={(e) => e.key === 'Escape' && dispatch('close')} />

<!-- svelte-ignore a11y-click-events-have-key-events a11y-no-static-element-interactions -->
<div class="overlay" on:click|self={() => dispatch('close')}>
  <div class="panel" style="transform: translate({dx}px, {dy}px)">
    <!-- svelte-ignore a11y-no-static-element-interactions -->
    <header on:pointerdown={onPointerDown}>
      <h2>Settings</h2>
      <button class="x" on:click={() => dispatch('close')} title="Close"><Icon name="x" /></button>
    </header>

    <div class="body">
      <nav class="snav">
        <button class:on={section === 'system'} on:click={() => (section = 'system')}>
          <Icon name="cpu" size={16} /> System
        </button>
        <button class:on={section === 'updates'} on:click={() => (section = 'updates')}>
          <Icon name="download2" size={16} /> Updates
        </button>
        <button class:on={section === 'appearance'} on:click={() => (section = 'appearance')}>
          <Icon name="sun" size={16} /> Style
        </button>
      </nav>

      <div class="content">
        {#if section === 'system'}
          <SystemPanel />
        {:else if section === 'updates'}
          <UpdatesPanel />
        {:else}
          <div class="group">
            <span class="glabel">Theme</span>
            <div class="themes">
              {#each themes as [id, name, bg, fg, accent]}
                <button class="theme" class:active={theme === id} on:click={() => dispatch('theme', id)}>
                  <span class="swatch" style="background:{bg}; color:{fg}">
                    Aa<span class="acc" style="background:{accent}"></span>
                  </span>
                  <span class="tname">{name}</span>
                  {#if theme === id}<span class="check"><Icon name="check" size={15} /></span>{/if}
                </button>
              {/each}
            </div>
          </div>

          <!-- UI scale is desktop-only; hidden on phones via the media query below. -->
          <div class="group scale-group">
            <span class="glabel">UI Scale</span>
            <div class="scales">
              {#each scales as [val, label]}
                <button
                  class="scalebtn"
                  class:active={Math.abs(scale - val) < 0.001}
                  on:click={() => dispatch('scale', val)}
                >
                  <span class="pct">{Math.round(val * 100)}%</span>
                  <span class="slabel">{label}</span>
                </button>
              {/each}
            </div>
            <p class="hint">Scales fonts and spacing across the whole dashboard.</p>
          </div>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 60;
    padding: 2rem;
  }
  .panel {
    width: 760px;
    max-width: 100%;
    height: 78vh;
    max-height: 640px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 10px;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.85rem 1.1rem;
    border-bottom: 1px solid var(--surface-2);
    flex-shrink: 0;
    cursor: move;
    user-select: none;
    touch-action: none;
  }
  .x { cursor: pointer; }
  h2 { font-size: 1rem; color: var(--text); }
  .x { background: none; border: none; color: var(--text-muted); cursor: pointer; display: inline-flex; }
  .x:hover { color: var(--red); }

  .body { flex: 1; display: flex; min-height: 0; }
  .snav {
    width: 168px;
    flex-shrink: 0;
    border-right: 1px solid var(--surface-2);
    padding: 0.75rem;
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    background: var(--bg-side);
  }
  .snav button {
    display: flex;
    align-items: center;
    gap: 0.55rem;
    background: none;
    border: none;
    color: var(--text-2);
    font-family: inherit;
    font-size: 0.88rem;
    text-align: left;
    padding: 0.5rem 0.7rem;
    border-radius: 5px;
    cursor: pointer;
  }
  .snav button:hover { background: var(--surface-2); color: var(--text); }
  .snav button.on { background: var(--surface-3); color: var(--text); }

  .content { flex: 1; min-width: 0; padding: 1.2rem; overflow-y: auto; }

  .glabel { color: var(--text-muted); font-size: 0.72rem; text-transform: uppercase; letter-spacing: 1px; }
  .themes { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.75rem; margin-top: 0.75rem; }
  .theme {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.5rem;
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.85rem 0.5rem;
    cursor: pointer;
    color: var(--text-2);
    font-family: inherit;
    font-size: 0.82rem;
  }
  .theme:hover { border-color: var(--border-3); }
  .theme.active { border-color: var(--blue); color: var(--text); }
  .swatch {
    position: relative;
    width: 100%;
    height: 42px;
    border-radius: 5px;
    border: 1px solid var(--border-2);
    display: flex;
    align-items: center;
    justify-content: center;
    font-weight: 700;
    font-size: 0.95rem;
  }
  .acc { position: absolute; bottom: 5px; right: 5px; width: 10px; height: 10px; border-radius: 50%; }
  .check { position: absolute; top: 6px; right: 6px; color: var(--blue); display: inline-flex; }

  .group + .group { margin-top: 1.6rem; }

  .scales { display: grid; grid-template-columns: repeat(5, 1fr); gap: 0.5rem; margin-top: 0.75rem; }
  .scalebtn {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.2rem;
    background: var(--surface-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.7rem 0.4rem;
    cursor: pointer;
    color: var(--text-2);
    font-family: inherit;
  }
  .scalebtn:hover { border-color: var(--border-3); }
  .scalebtn.active { border-color: var(--blue); color: var(--text); background: var(--surface-3); }
  .pct { font-size: 1rem; font-weight: 700; }
  .slabel { font-size: 0.7rem; color: var(--text-muted); }
  .scalebtn.active .slabel { color: var(--text-2); }
  .hint { margin-top: 0.65rem; font-size: 0.74rem; color: var(--text-muted); }

  @media (max-width: 640px) {
    .overlay { padding: 0; }
    .panel { width: 100%; height: 100%; max-height: none; border-radius: 0; transform: none !important; }
    .snav { width: 124px; }
    .themes { grid-template-columns: repeat(2, 1fr); }
    header { cursor: default; }
    /* UI scale is desktop-only — hide the control entirely on phones. */
    .scale-group { display: none; }
  }
</style>

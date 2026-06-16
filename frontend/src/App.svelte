<script lang="ts">
  import { onMount } from 'svelte';
  import Sidebar from './components/Sidebar.svelte';
  import Overview from './pages/Overview.svelte';
  import Terminal from './pages/Terminal.svelte';
  import Media from './pages/Media.svelte';
  import FilesFull from './pages/FilesFull.svelte';
  import Docker from './pages/Docker.svelte';
  import Hub from './pages/Hub.svelte';
  import Services from './pages/Services.svelte';
  import Login from './pages/Login.svelte';
  import Settings from './components/Settings.svelte';
  import Network from './pages/Network.svelte';
  import Assistant from './pages/Assistant.svelte';

  let page = 'hub';

  let ready = false;
  let authed = false;
  let authEnabled = false;
  let showSettings = false;

  // Theme: applied to <html data-theme> and persisted. Set immediately (not in
  // onMount) to avoid a flash of the wrong theme.
  function readTheme(): string {
    try {
      return localStorage.getItem('theme') || 'dark';
    } catch {
      return 'dark';
    }
  }
  let theme = readTheme();
  if (typeof document !== 'undefined') document.documentElement.setAttribute('data-theme', theme);

  function applyTheme(t: string) {
    theme = t;
    document.documentElement.setAttribute('data-theme', t);
    try {
      localStorage.setItem('theme', t);
    } catch {
      /* private mode */
    }
  }

  // UI scale: a unitless multiplier exposed as --ui-scale on <html>. The global
  // stylesheet turns it into the root font-size, but only above the mobile
  // breakpoint, so phones always render at the native 1x. Set immediately to
  // avoid a flash at the wrong size.
  function readScale(): number {
    try {
      const n = parseFloat(localStorage.getItem('ui-scale') || '1');
      return Number.isFinite(n) ? Math.min(2, Math.max(0.5, n)) : 1;
    } catch {
      return 1;
    }
  }
  let scale = readScale();
  if (typeof document !== 'undefined')
    document.documentElement.style.setProperty('--ui-scale', String(scale));

  function applyScale(s: number) {
    scale = s;
    document.documentElement.style.setProperty('--ui-scale', String(s));
    try {
      localStorage.setItem('ui-scale', String(s));
    } catch {
      /* private mode */
    }
  }

  async function checkAuth() {
    try {
      const d = await (await fetch('/api/me')).json();
      authEnabled = !!d.auth_enabled;
      authed = !d.auth_enabled || !!d.authenticated;
    } catch {
      authed = false;
    }
    ready = true;
  }

  async function logout() {
    try {
      await fetch('/api/logout', { method: 'POST' });
    } catch {
      /* ignore */
    }
    authed = false;
  }

  onMount(checkAuth);
</script>

{#if !ready}
  <div class="boot"></div>
{:else if !authed}
  <Login on:success={() => (authed = true)} />
{:else}
  <div class="app">
    <Sidebar
      showLogout={authEnabled}
      active={page}
      on:change={(e) => (page = e.detail)}
      on:logout={logout}
      on:settings={() => (showSettings = true)}
    />
    <main class="content">
      {#if page === 'hub'}<Hub />
      {:else if page === 'overview'}<Overview />
      {:else if page === 'terminal'}<Terminal />
      {:else if page === 'media'}<Media />
      {:else if page === 'files'}<FilesFull />
      {:else if page === 'docker'}<Docker />
      {:else if page === 'services'}<Services />
      {:else if page === 'network'}<Network />
      {:else if page === 'assistant'}<Assistant />
      {/if}
    </main>
  </div>
  {#if showSettings}
    <Settings
      {theme}
      {scale}
      on:theme={(e) => applyTheme(e.detail)}
      on:scale={(e) => applyScale(e.detail)}
      on:close={() => (showSettings = false)}
    />
  {/if}
{/if}

<style>
  /* ===== theme palettes =====
     :root is the default "Dark" (near-black). Other themes override.
     Accents: --blue --green --red --peach --mauve, plus soft accent
     surfaces --green-bg/-bd and --red-bg/-bd. */
  :global(:root) {
    --bg: #0d0d0f;
    --bg-side: #111114;
    --surface: #18181b;
    --surface-2: #1e1e24;
    --surface-3: #26262e;
    --border: #313244;
    --border-2: #45475a;
    --border-3: #585b70;
    --text: #cdd6f4;
    --text-2: #a6adc8;
    --text-muted: #6c7086;
    --text-dim: #7f849c;
    --blue: #89b4fa;
    --green: #a6e3a1;
    --red: #f38ba8;
    --peach: #fab387;
    --mauve: #cba6f7;
    --green-bg: #2a3a2a;
    --green-bd: #40553f;
    --red-bg: #2a1a1f;
    --red-bd: #5a2a35;
  }
  /* Catppuccin Mocha — the classic #1e1e2e blue-grey */
  :global([data-theme='mocha']) {
    --bg: #11111b;
    --bg-side: #181825;
    --surface: #1e1e2e;
    --surface-2: #313244;
    --surface-3: #45475a;
    --border: #313244;
    --border-2: #45475a;
    --border-3: #585b70;
    --text: #cdd6f4;
    --text-2: #bac2de;
    --text-muted: #7f849c;
    --text-dim: #6c7086;
  }
  /* Tokyo Night */
  :global([data-theme='tokyo']) {
    --bg: #1a1b26;
    --bg-side: #16161e;
    --surface: #1f2335;
    --surface-2: #24283b;
    --surface-3: #2f334d;
    --border: #3b4261;
    --border-2: #545c7e;
    --border-3: #737aa2;
    --text: #c0caf5;
    --text-2: #a9b1d6;
    --text-muted: #787c99;
    --text-dim: #565f89;
    --blue: #7aa2f7;
    --green: #9ece6a;
    --red: #f7768e;
    --peach: #ff9e64;
    --mauve: #bb9af7;
    --green-bg: #1f2d24;
    --green-bd: #2e4636;
    --red-bg: #2d1f26;
    --red-bd: #4a2d3a;
  }
  /* Dim — a medium slate grey, the midpoint between Light and Dark */
  :global([data-theme='dim']) {
    --bg: #2e3036;
    --bg-side: #292b30;
    --surface: #383b42;
    --surface-2: #42454d;
    --surface-3: #4c4f58;
    --border: #4c4f58;
    --border-2: #5a5e68;
    --border-3: #6b6f7a;
    --text: #dfe1e8;
    --text-2: #c4c7d0;
    --text-muted: #9499a5;
    --text-dim: #80858f;
    --blue: #8caaee;
    --green: #a6d189;
    --red: #e78284;
    --peach: #ef9f76;
    --mauve: #ca9ee6;
    --green-bg: #34402f;
    --green-bd: #4a5a3f;
    --red-bg: #40302f;
    --red-bd: #5a423f;
  }
  :global([data-theme='amoled']) {
    --bg: #000000;
    --bg-side: #000000;
    --surface: #0a0a0c;
    --surface-2: #141417;
    --surface-3: #1c1c20;
    --border: #222228;
    --border-2: #33333a;
    --border-3: #44444c;
    --green-bg: #16241a;
    --green-bd: #2c4a35;
    --red-bg: #241318;
    --red-bd: #4a2330;
  }
  /* Catppuccin Latte — light, with darker accents for contrast on white */
  :global([data-theme='light']) {
    --bg: #eff1f5;
    --bg-side: #e6e9ef;
    --surface: #ffffff;
    --surface-2: #e6e9ef;
    --surface-3: #dce0e8;
    --border: #ccd0da;
    --border-2: #bcc0cc;
    --border-3: #9ca0b0;
    --text: #4c4f69;
    --text-2: #5c5f77;
    --text-muted: #8c8fa1;
    --text-dim: #7c7f93;
    --blue: #1e66f5;
    --green: #40a02b;
    --red: #d20f39;
    --peach: #fe640b;
    --mauve: #8839ef;
    --green-bg: #e6f3e6;
    --green-bd: #a6d9a6;
    --red-bg: #fbe3e7;
    --red-bd: #f0a8b5;
  }
  /* Rosé Pine Dawn — warm, low-contrast light with a rosy cast */
  :global([data-theme='dawn']) {
    --bg: #faf4ed;
    --bg-side: #f2e9e1;
    --surface: #fffaf3;
    --surface-2: #f2e9e1;
    --surface-3: #e9e1da;
    --border: #dfdad9;
    --border-2: #cecacd;
    --border-3: #9893a5;
    --text: #575279;
    --text-2: #6e6a86;
    --text-muted: #908caa;
    --text-dim: #9893a5;
    --blue: #286983;
    --green: #568259;
    --red: #b4637a;
    --peach: #ea9d34;
    --mauve: #907aa9;
    --green-bg: #e8efe6;
    --green-bd: #b9d2b5;
    --red-bg: #f6e2e6;
    --red-bd: #e6b3bf;
  }
  /* Solarized Light — Ethan Schoonover's classic sepia/cream */
  :global([data-theme='solarized']) {
    --bg: #f6efdc;
    --bg-side: #eee8d5;
    --surface: #fdf6e3;
    --surface-2: #eee8d5;
    --surface-3: #e3ddca;
    --border: #ddd6c1;
    --border-2: #ccc6b0;
    --border-3: #93a1a1;
    --text: #586e75;
    --text-2: #657b83;
    --text-muted: #93a1a1;
    --text-dim: #839496;
    --blue: #268bd2;
    --green: #859900;
    --red: #dc322f;
    --peach: #cb4b16;
    --mauve: #6c71c4;
    --green-bg: #eef0d8;
    --green-bd: #c9d39a;
    --red-bg: #f7e3d9;
    --red-bd: #e9b8a0;
  }
  /* Everforest Light — soft, easy-on-the-eyes sage green */
  :global([data-theme='everforest']) {
    --bg: #f4f0da;
    --bg-side: #efebd4;
    --surface: #fffbef;
    --surface-2: #efebd4;
    --surface-3: #e5e1cb;
    --border: #e0dcc7;
    --border-2: #d2cdb6;
    --border-3: #a6b0a0;
    --text: #5c6a72;
    --text-2: #708089;
    --text-muted: #939f91;
    --text-dim: #829181;
    --blue: #3a94c5;
    --green: #8da101;
    --red: #f85552;
    --peach: #f57d26;
    --mauve: #df69ba;
    --green-bg: #e9eed3;
    --green-bd: #c7d49a;
    --red-bg: #f7e0db;
    --red-bd: #efb9b1;
  }

  :global(*) { box-sizing: border-box; margin: 0; padding: 0; }
  :global(html, body) { height: 100%; }
  :global(body) {
    background: var(--bg);
    color: var(--text);
    font-family: 'JetBrains Mono', monospace;
    overflow: hidden;
    transition: background 0.2s, color 0.2s;
  }
  .app { display: flex; height: 100vh; }
  .content { flex: 1; padding: 2rem; overflow-y: auto; position: relative; }
  .boot { height: 100vh; background: var(--bg); }

  /* Full-height pages use this so they fit on both desktop and mobile. */
  :global(:root) { --page-h: calc(100vh - 4rem); }

  /* ===== UI scale (Minecraft-style GUI scale) =====
     Everything is sized in rem, so scaling the root font-size scales the whole
     dashboard — fonts, padding, gaps, gauges. Gated to the desktop breakpoint so
     phones always render at 1x regardless of any stored value. */
  @media (min-width: 641px) {
    :global(html) {
      font-size: calc(16px * var(--ui-scale, 1));
      transition: font-size 0.15s ease;
    }
  }

  /* ===== mobile ===== */
  @media (max-width: 640px) {
    :global(:root) { --page-h: calc(100vh - 5.5rem); }
    .content { padding: 1rem 1rem 4.5rem; }
  }
</style>

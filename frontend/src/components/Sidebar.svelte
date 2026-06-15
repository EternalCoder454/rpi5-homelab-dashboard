<script lang="ts">
  import { createEventDispatcher } from 'svelte';
  import Icon from './Icon.svelte';

  function loadCollapsed(): boolean {
    try {
      return localStorage.getItem('sb_collapsed') === '1';
    } catch {
      return false;
    }
  }
  let collapsed = loadCollapsed();
  function toggleCollapsed() {
    collapsed = !collapsed;
    try {
      localStorage.setItem('sb_collapsed', collapsed ? '1' : '0');
    } catch {
      /* private mode: ignore */
    }
  }

  export let showLogout = false;
  export let active = '';

  const dispatch = createEventDispatcher<{ change: string; logout: void; settings: void }>();
  const go = (p: string) => dispatch('change', p);

  const items: Array<[string, string, string]> = [
    ['hub', 'home', 'Home'],
    ['overview', 'overview', 'Overview'],
    ['terminal', 'terminal', 'Terminal'],
    ['media', 'image', 'Media'],
    ['files', 'folder', 'Files'],
    ['docker', 'box', 'Docker'],
    ['services', 'server', 'Services'],
    ['network', 'network', 'Network'],
  ];
</script>

<aside class="sidebar" class:collapsed>
  <button class="toggle" on:click={toggleCollapsed} title="Toggle sidebar">
    <Icon name="menu" size={20} />
  </button>
  <nav>
    {#each items as [p, icon, label]}
      <button class:active={p === active} on:click={() => go(p)} title={label}>
        <span class="icon"><Icon name={icon} size={18} /></span>
        {#if !collapsed}<span class="label">{label}</span>{/if}
      </button>
    {/each}
  </nav>

  <div class="footer">
    <button class="footbtn" on:click={() => dispatch('settings')} title="Settings">
      <span class="icon"><Icon name="settings" size={18} /></span>
      {#if !collapsed}<span class="label">Settings</span>{/if}
    </button>
    {#if showLogout}
      <button class="footbtn" on:click={() => dispatch('logout')} title="Sign out">
        <span class="icon"><Icon name="log-out" size={18} /></span>
        {#if !collapsed}<span class="label">Sign out</span>{/if}
      </button>
    {/if}
  </div>
</aside>

<style>
  .sidebar {
    width: 200px;
    background: var(--bg-side);
    border-right: 1px solid var(--surface-2);
    display: flex;
    flex-direction: column;
    transition: width 0.2s;
  }
  .collapsed { width: 50px; }
  .toggle {
    align-self: flex-end;
    background: none;
    border: none;
    color: var(--text);
    cursor: pointer;
    padding: 1rem;
    font-size: 1.2rem;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  nav { display: flex; flex-direction: column; gap: 0.5rem; padding: 1rem; }
  button {
    background: none;
    border: none;
    color: var(--text-2);
    cursor: pointer;
    text-align: left;
    font-family: inherit;
    font-size: 0.95rem;
    padding: 0.5rem;
    display: flex;
    align-items: center;
    gap: 0.6rem;
    border-radius: 4px;
  }
  button:hover { color: var(--red); background: var(--surface); }
  nav button.active { background: var(--surface-3); color: var(--text); box-shadow: inset 3px 0 0 0 var(--blue); }
  nav button.active:hover { color: var(--text); }
  .icon { display: inline-flex; align-items: center; justify-content: center; width: 1.3rem; flex-shrink: 0; }

  .footer { margin-top: auto; margin-bottom: 0.5rem; display: flex; flex-direction: column; gap: 0.25rem; padding: 0 1rem; }
  .footbtn { color: var(--text-muted); }
  .footbtn:hover { color: var(--red); background: var(--surface); }

  /* compact mode */
  .collapsed nav { align-items: center; padding: 1rem 0.4rem; }
  .collapsed button { justify-content: center; padding: 0.5rem 0; width: 100%; }
  .collapsed .footer { padding: 0 0.4rem; align-items: center; }
  .collapsed nav button.active { background: none; box-shadow: none; }
  .collapsed nav button.active .icon { position: relative; }
  .collapsed nav button.active .icon::after {
    content: '';
    position: absolute;
    left: 50%;
    bottom: -7px;
    transform: translateX(-50%);
    width: 16px;
    height: 2px;
    border-radius: 1px;
    background: var(--blue);
  }

  /* ===== mobile: a fixed bottom nav bar ===== */
  @media (max-width: 640px) {
    .sidebar {
      width: auto;
      height: 3.5rem;
      flex-direction: row;
      align-items: stretch;
      position: fixed;
      left: 0;
      right: 0;
      bottom: 0;
      border-right: none;
      border-top: 1px solid var(--surface-2);
      z-index: 40;
    }
    .toggle { display: none; }
    /* flatten nav + footer so all icons sit evenly in one row */
    nav,
    .footer {
      display: contents;
    }
    .sidebar nav button,
    .sidebar .footbtn {
      flex: 1;
      flex-direction: column;
      justify-content: center;
      gap: 0;
      margin: 0;
      padding: 0;
      border-radius: 0;
    }
    .label { display: none; }
    nav button.active { background: none; box-shadow: inset 0 2px 0 0 var(--blue); }
    nav button.active .icon::after { display: none; }
  }
</style>

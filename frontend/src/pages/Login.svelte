<script lang="ts">
  import { createEventDispatcher } from 'svelte';

  const dispatch = createEventDispatcher<{ success: void }>();

  let username = '';
  let password = '';
  let error = '';
  let busy = false;

  async function submit() {
    if (busy) return;
    busy = true;
    error = '';
    try {
      const r = await fetch('/api/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      });
      if (!r.ok) {
        error = r.status === 401 ? 'Invalid username or password' : 'Login failed';
        busy = false;
        return;
      }
      dispatch('success');
    } catch {
      error = 'Could not reach the server';
      busy = false;
    }
  }
</script>

<div class="wrap">
  <form class="card" on:submit|preventDefault={submit}>
    <div class="brand">📊 Homelab</div>
    <p class="sub">Sign in to continue</p>

    <input
      placeholder="Username"
      bind:value={username}
      autocomplete="username"
      autocapitalize="off"
      spellcheck="false"
    />
    <input
      type="password"
      placeholder="Password"
      bind:value={password}
      autocomplete="current-password"
    />

    {#if error}<div class="err">{error}</div>{/if}

    <button type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
  </form>
</div>

<style>
  .wrap {
    height: 100vh;
    display: flex;
    align-items: center;
    justify-content: center;
    background: var(--bg);
  }
  .card {
    width: 320px;
    display: flex;
    flex-direction: column;
    gap: 0.85rem;
    background: var(--surface);
    border: 1px solid var(--surface-2);
    border-radius: 10px;
    padding: 2rem;
  }
  .brand { font-size: 1.4rem; font-weight: 700; color: var(--text); text-align: center; }
  .sub { color: var(--text-muted); text-align: center; font-size: 0.85rem; margin-bottom: 0.5rem; }
  input {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 6px;
    color: var(--text);
    font-family: inherit;
    font-size: 0.9rem;
    padding: 0.65rem 0.8rem;
  }
  input:focus { outline: none; border-color: var(--border-3); }
  .err {
    color: var(--red);
    background: var(--red-bg);
    border: 1px solid var(--red-bd);
    border-radius: 6px;
    padding: 0.5rem 0.7rem;
    font-size: 0.82rem;
  }
  button {
    margin-top: 0.3rem;
    background: var(--border);
    border: 1px solid var(--border-2);
    border-radius: 6px;
    color: var(--text);
    font-family: inherit;
    font-size: 0.9rem;
    padding: 0.65rem;
    cursor: pointer;
  }
  button:hover:not(:disabled) { background: var(--surface-3); }
  button:disabled { opacity: 0.6; cursor: default; }
</style>

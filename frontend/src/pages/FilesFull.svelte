<script lang="ts">
  import { onMount } from 'svelte';
  import CodeEditor from '../components/CodeEditor.svelte';
  import Icon from '../components/Icon.svelte';

  interface Entry {
    name: string;
    path: string;
    is_dir: boolean;
    size: number;
    mod_time: number;
  }

  let checking = true;
  let unlocked = false;
  let password = '';
  let unlockErr = '';

  let cwd = '/';
  let entries: Entry[] = [];
  let err = '';
  let loading = false;
  let editing: { path: string; name: string; content: string } | null = null;

  async function checkStatus() {
    try {
      const d = await (await fetch('/api/fs/status')).json();
      unlocked = !!d.unlocked;
      if (unlocked) await load('/');
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
    checking = false;
  }

  async function unlock() {
    unlockErr = '';
    try {
      const r = await fetch('/api/fs/unlock', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password }),
      });
      if (!r.ok) throw new Error((await r.text()).trim() || 'Wrong password.');
      password = '';
      unlocked = true;
      await load('/');
    } catch (e) {
      unlockErr = String(e instanceof Error ? e.message : e);
    }
  }

  async function load(p = cwd) {
    loading = true;
    err = '';
    try {
      const r = await fetch('/api/fs/list?path=' + encodeURIComponent(p));
      if (r.status === 403) {
        unlocked = false;
        loading = false;
        return;
      }
      if (!r.ok) throw new Error((await r.text()).trim());
      entries = await r.json();
      cwd = p;
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
    loading = false;
  }

  const dlUrl = (p: string) => '/api/fs/download?path=' + encodeURIComponent(p);

  async function openEntry(e: Entry) {
    if (e.is_dir) {
      load(e.path);
      return;
    }
    err = '';
    try {
      const r = await fetch('/api/fs/read?path=' + encodeURIComponent(e.path));
      if (r.ok) {
        const d = await r.json();
        editing = { path: e.path, name: e.name, content: d.content };
        return;
      }
      if (r.status === 415 || r.status === 413) {
        err = e.name + ': ' + (await r.text()).trim() + ' — use Download.';
        return;
      }
      throw new Error((await r.text()).trim());
    } catch (er) {
      err = String(er instanceof Error ? er.message : er);
    }
  }

  async function saveFile(content: string) {
    if (!editing) return;
    try {
      const r = await fetch('/api/fs/write', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path: editing.path, content }),
      });
      if (!r.ok) throw new Error((await r.text()).trim());
      editing = { ...editing, content };
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  const parentOf = (p: string) => {
    if (p === '/' || p === '') return '/';
    const i = p.lastIndexOf('/');
    return i <= 0 ? '/' : p.slice(0, i);
  };
  function fmtSize(n: number): string {
    if (n < 1024) return `${n} B`;
    if (n < 1048576) return `${(n / 1024).toFixed(1)} KB`;
    if (n < 1073741824) return `${(n / 1048576).toFixed(1)} MB`;
    return `${(n / 1073741824).toFixed(1)} GB`;
  }
  const fmtDate = (u: number) => new Date(u * 1000).toLocaleString();

  $: crumbs = (() => {
    const out = [{ label: '/', path: '/' }];
    let acc = '';
    for (const part of cwd.split('/').filter(Boolean)) {
      acc += '/' + part;
      out.push({ label: part, path: acc });
    }
    return out;
  })();

  onMount(checkStatus);
</script>

<div class="fs">
  {#if checking}
    <div class="boot"></div>
  {:else if !unlocked}
    <div class="lock">
      <div class="lockbox">
        <span class="lockic"><Icon name="lock" size={26} /></span>
        <h2>Filesystem</h2>
        <p class="lp">Browsing the whole Pi requires your password again.</p>
        <input type="password" placeholder="Password" bind:value={password} on:keydown={(e) => e.key === 'Enter' && unlock()} />
        <button class="unlockbtn" on:click={unlock} disabled={!password}>Unlock</button>
        {#if unlockErr}<div class="lerr">{unlockErr}</div>{/if}
      </div>
    </div>
  {:else if editing}
    {#key editing.path}
      <CodeEditor filename={editing.name} content={editing.content} on:save={(e) => saveFile(e.detail)} on:close={() => (editing = null)} />
    {/key}
  {:else}
    <header class="bar">
      <nav class="crumbs">
        {#each crumbs as c, i}
          {#if i > 0}<span class="slash">/</span>{/if}
          <button class={i === 0 ? 'title-crumb' : 'crumb'} on:click={() => load(c.path)}>{c.label}</button>
        {/each}
      </nav>
      <button class="ghost" on:click={() => load()} title="Refresh"><Icon name="refresh" /></button>
    </header>

    {#if err}<div class="err" role="alert">⚠ {err} <button class="dismiss" on:click={() => (err = '')}>dismiss</button></div>{/if}

    <div class="list">
      <table>
        <thead><tr><th>Name</th><th class="num">Size</th><th>Modified</th><th class="act"></th></tr></thead>
        <tbody>
          {#if cwd !== '/'}
            <tr class="up" on:click={() => load(parentOf(cwd))}><td colspan="4"><span class="ic"><Icon name="corner-up-left" /></span> ..</td></tr>
          {/if}
          {#each entries as e (e.path)}
            <tr>
              <td class="name-cell">
                <span class="ic" style="color:{e.is_dir ? 'var(--blue)' : 'var(--text-dim)'}"><Icon name={e.is_dir ? 'folder' : 'file'} /></span>
                <button class="link" on:click={() => openEntry(e)}>{e.name}</button>
              </td>
              <td class="num">{e.is_dir ? '—' : fmtSize(e.size)}</td>
              <td class="date">{fmtDate(e.mod_time)}</td>
              <td class="act">
                {#if !e.is_dir}<a class="iconbtn" title="Download" href={dlUrl(e.path)}><Icon name="download" /></a>{/if}
              </td>
            </tr>
          {/each}
          {#if !loading && entries.length === 0}<tr><td colspan="4" class="empty">empty</td></tr>{/if}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<style>
  .fs { position: relative; height: var(--page-h); display: flex; flex-direction: column; }
  .boot { flex: 1; }

  .lock { flex: 1; display: flex; align-items: center; justify-content: center; }
  .lockbox { display: flex; flex-direction: column; align-items: center; gap: 0.5rem; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; padding: 2rem 2.25rem; width: 320px; max-width: 100%; }
  .lockic { color: var(--peach); }
  .lockbox h2 { font-size: 1.1rem; }
  .lp { color: var(--text-muted); font-size: 0.82rem; text-align: center; margin-bottom: 0.5rem; }
  .lockbox input { width: 100%; background: var(--bg); border: 1px solid var(--border); border-radius: 6px; color: var(--text); font-family: inherit; font-size: 0.9rem; padding: 0.55rem 0.7rem; }
  .unlockbtn { width: 100%; background: var(--green-bg); border: 1px solid var(--green-bd); color: var(--green); font-family: inherit; font-size: 0.9rem; padding: 0.55rem; border-radius: 6px; cursor: pointer; }
  .unlockbtn:disabled { opacity: 0.5; cursor: default; }
  .lerr { color: var(--red); font-size: 0.8rem; }

  .bar { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: 1rem; flex-wrap: wrap; }
  .crumbs { display: flex; align-items: baseline; gap: 0.25rem; font-size: 0.9rem; flex-wrap: wrap; }
  .title-crumb { background: none; border: none; color: var(--text); cursor: pointer; font-family: inherit; font-size: 1.3rem; font-weight: 600; padding: 0; }
  .crumb { background: none; border: none; color: var(--blue); cursor: pointer; font-family: inherit; font-size: 0.9rem; padding: 0.2rem 0.3rem; }
  .crumb:hover { text-decoration: underline; }
  .slash { color: var(--border-2); }
  .ghost { background: var(--surface); border: 1px solid var(--border); color: var(--text-muted); padding: 0.4rem 0.55rem; border-radius: 5px; cursor: pointer; display: inline-flex; }
  .ghost:hover { border-color: var(--border-3); color: var(--text); }

  .err { background: var(--red-bg); border: 1px solid var(--red-bd); color: var(--red); padding: 0.6rem 0.9rem; border-radius: 6px; margin-bottom: 0.75rem; font-size: 0.85rem; overflow-wrap: anywhere; }
  .dismiss { background: none; border: none; color: var(--text-muted); font-family: inherit; font-size: 0.75rem; cursor: pointer; text-decoration: underline; }

  .list { flex: 1; overflow-y: auto; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
  table { width: 100%; border-collapse: collapse; }
  th, td { padding: 0.7rem 1rem; text-align: left; border-bottom: 1px solid var(--surface-2); font-size: 0.88rem; }
  th { color: var(--text-muted); font-weight: normal; position: sticky; top: 0; background: var(--surface); z-index: 1; }
  th.num, td.num { text-align: right; width: 7rem; color: var(--text-2); }
  th.act, td.act { width: 4rem; text-align: right; }
  .date { color: var(--text-muted); font-size: 0.8rem; }
  .ic { margin-right: 0.5rem; display: inline-flex; align-items: center; }
  .name-cell { display: flex; align-items: center; }
  .link { background: none; border: none; color: var(--text); cursor: pointer; font-family: inherit; font-size: 0.88rem; padding: 0; text-align: left; }
  .link:hover { color: var(--blue); }
  .up { cursor: pointer; color: var(--text-2); }
  .up:hover { background: var(--surface-2); }
  .empty { color: var(--text-muted); text-align: center; padding: 2rem; }
  .iconbtn { display: inline-flex; align-items: center; justify-content: center; padding: 0.32rem; border: none; background: none; color: var(--text-dim); border-radius: 4px; cursor: pointer; text-decoration: none; }
  .iconbtn:hover { background: var(--surface-2); color: var(--text); }

  @media (max-width: 640px) {
    .list { overflow-x: auto; }
    table th:nth-child(3), table td:nth-child(3) { display: none; }
  }
</style>

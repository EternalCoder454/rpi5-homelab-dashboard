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

  let cwd = '';
  let entries: Entry[] = [];
  let err = '';
  let loading = false;

  let editing: { path: string; name: string; content: string } | null = null;
  let viewing: { kind: string; name: string; path: string } | null = null;

  const IMG = ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif'];
  const VID = ['mp4', 'webm', 'mov', 'm4v', 'ogv'];
  const AUD = ['mp3', 'wav', 'ogg', 'flac', 'm4a', 'aac', 'opus'];
  function kindOf(name: string): string {
    const ext = name.split('.').pop()?.toLowerCase() || '';
    if (IMG.includes(ext)) return 'image';
    if (VID.includes(ext)) return 'video';
    if (AUD.includes(ext)) return 'audio';
    return 'text';
  }
  const rawUrl = (p: string) => '/api/files/raw?path=' + encodeURIComponent(p);
  const dlUrl = (p: string) => '/api/files/download?path=' + encodeURIComponent(p);

  function openFile(e: Entry) {
    err = '';
    const k = kindOf(e.name);
    if (k === 'text') editFile(e);
    else viewing = { kind: k, name: e.name, path: e.path };
  }

  // inline create / rename / delete state
  let creating: '' | 'file' | 'folder' = '';
  let newName = '';
  let renamingPath: string | null = null;
  let renameValue = '';
  let confirmDelete: string | null = null;

  // upload
  let uploading = false;
  let progress = 0;
  let fileInput: HTMLInputElement;

  const join = (dir: string, name: string) => (dir ? `${dir}/${name}` : name);
  const parentOf = (p: string) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '');

  function fmtSize(n: number): string {
    if (n < 1024) return `${n} B`;
    if (n < 1048576) return `${(n / 1024).toFixed(1)} KB`;
    if (n < 1073741824) return `${(n / 1048576).toFixed(1)} MB`;
    return `${(n / 1073741824).toFixed(1)} GB`;
  }
  function fmtDate(unix: number): string {
    return new Date(unix * 1000).toLocaleString();
  }

  $: crumbs = (() => {
    const out = [{ label: 'Files', path: '' }];
    let acc = '';
    for (const part of cwd.split('/').filter(Boolean)) {
      acc = acc ? `${acc}/${part}` : part;
      out.push({ label: part, path: acc });
    }
    return out;
  })();

  async function load(p = cwd) {
    loading = true;
    err = '';
    try {
      const r = await fetch('/api/files/list?path=' + encodeURIComponent(p));
      if (!r.ok) throw new Error((await r.text()).trim());
      const d = await r.json();
      entries = Array.isArray(d) ? d : [];
      cwd = p;
      resetTransient();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
    loading = false;
  }

  function resetTransient() {
    creating = '';
    newName = '';
    renamingPath = null;
    confirmDelete = null;
  }

  async function post(url: string, body: unknown) {
    const r = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error((await r.text()).trim() || r.statusText);
    return r.json();
  }

  function openEntry(e: Entry) {
    if (e.is_dir) load(e.path);
    else openFile(e);
  }

  async function editFile(e: { path: string; name: string }) {
    err = '';
    try {
      const r = await fetch('/api/files/read?path=' + encodeURIComponent(e.path));
      if (r.status === 415) {
        // server says it's binary — offer a download fallback in the viewer
        viewing = { kind: 'binary', name: e.name, path: e.path };
        return;
      }
      if (!r.ok) throw new Error((await r.text()).trim());
      const d = await r.json();
      editing = { path: e.path, name: e.name, content: d.content };
    } catch (e2) {
      err = String(e2 instanceof Error ? e2.message : e2);
    }
  }

  async function saveFile(content: string) {
    if (!editing) return;
    try {
      await post('/api/files/write', { path: editing.path, content });
      editing = { ...editing, content }; // new baseline -> editor clears "modified"
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  async function submitCreate() {
    const name = newName.trim();
    if (!name) return;
    try {
      await post('/api/files/create', { path: join(cwd, name), dir: creating === 'folder' });
      await load();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
  }

  async function submitRename(e: Entry) {
    const name = renameValue.trim();
    if (!name || name === e.name) {
      renamingPath = null;
      return;
    }
    try {
      await post('/api/files/rename', { from: e.path, to: join(parentOf(e.path), name) });
      await load();
    } catch (er) {
      err = String(er instanceof Error ? er.message : er);
    }
  }

  async function doDelete(e: Entry) {
    try {
      await post('/api/files/delete', { path: e.path });
      await load();
    } catch (er) {
      err = String(er instanceof Error ? er.message : er);
    }
  }

  function startRename(e: Entry) {
    renamingPath = e.path;
    renameValue = e.name;
    confirmDelete = null;
  }

  function handleUpload(ev: Event) {
    const input = ev.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    uploading = true;
    progress = 0;
    const xhr = new XMLHttpRequest();
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) progress = Math.round((e.loaded / e.total) * 100);
    };
    xhr.onload = () => {
      uploading = false;
      progress = 0;
      input.value = '';
      load();
    };
    xhr.onerror = () => {
      uploading = false;
      err = 'Upload failed';
    };
    const fd = new FormData();
    fd.append('file', file);
    fd.append('dir', cwd);
    xhr.open('POST', '/api/upload');
    xhr.send(fd);
  }

  // --- media categories (gallery / videos / documents) ---
  type MediaEntry = { name: string; path: string; size: number; mod_time: number; cat: string; thumb: boolean };
  const TABS = [
    { key: 'all', label: 'All' },
    { key: 'gallery', label: 'Gallery' },
    { key: 'videos', label: 'Videos' },
    { key: 'documents', label: 'Documents' },
  ];
  let tab = 'all';
  let media: MediaEntry[] = [];
  let mediaLoading = false;
  const thumbUrl = (p: string) => '/api/media/thumb?path=' + encodeURIComponent(p);

  async function setTab(t: string) {
    tab = t;
    if (t !== 'all') await loadMedia(t);
  }
  async function loadMedia(cat: string) {
    mediaLoading = true;
    err = '';
    try {
      const r = await fetch('/api/media/scan?cat=' + cat);
      if (!r.ok) throw new Error((await r.text()).trim());
      media = await r.json();
    } catch (e) {
      err = String(e instanceof Error ? e.message : e);
    }
    mediaLoading = false;
  }
  function viewMedia(m: MediaEntry) {
    viewing = { kind: m.cat === 'video' ? 'video' : 'image', name: m.name, path: m.path };
  }

  onMount(() => load(''));
</script>

<div class="files">
  {#if editing}
    {#key editing.path}
      <CodeEditor
        filename={editing.name}
        content={editing.content}
        on:save={(e) => saveFile(e.detail)}
        on:close={() => (editing = null)}
      />
    {/key}
  {:else if viewing}
    <div class="viewer">
      <header class="vbar">
        <span class="vname">{viewing.name}</span>
        <div class="vactions">
          <a class="vbtn" href={dlUrl(viewing.path)}>Download</a>
          <button class="vbtn" on:click={() => (viewing = null)}>Close</button>
        </div>
      </header>
      <div class="vbody">
        {#if viewing.kind === 'image'}
          <img src={rawUrl(viewing.path)} alt={viewing.name} />
        {:else if viewing.kind === 'video'}
          <!-- svelte-ignore a11y-media-has-caption -->
          <video src={rawUrl(viewing.path)} controls></video>
        {:else if viewing.kind === 'audio'}
          <audio src={rawUrl(viewing.path)} controls></audio>
        {:else}
          <div class="vbinary">Can't preview this file type. <a href={dlUrl(viewing.path)}>Download it</a> instead.</div>
        {/if}
      </div>
    </div>
  {:else}
    <div class="mediatabs">
      {#each TABS as t}
        <button class:on={tab === t.key} on:click={() => setTab(t.key)}>{t.label}</button>
      {/each}
    </div>

    {#if tab === 'all'}
    <header class="bar">
      <nav class="crumbs">
        {#each crumbs as c, i}
          {#if i > 0}<span class="slash">/</span>{/if}
          <button class={i === 0 ? 'title-crumb' : 'crumb'} on:click={() => load(c.path)}>{c.label}</button>
        {/each}
      </nav>
      <div class="tools">
        <button on:click={() => { creating = 'file'; newName = ''; }}><Icon name="file-plus" /> File</button>
        <button on:click={() => { creating = 'folder'; newName = ''; }}><Icon name="folder-plus" /> Folder</button>
        <button on:click={() => fileInput.click()}><Icon name="upload" /> Upload</button>
        <button class="ghost" on:click={() => load()} title="Refresh"><Icon name="refresh" /></button>
      </div>
      <input bind:this={fileInput} type="file" on:change={handleUpload} hidden />
    </header>

    {#if uploading}<div class="upbar"><span style="width:{progress}%"></span></div>{/if}
    {#if err}
      <div class="err" role="alert">
        ⚠ {err}
        <button class="dismiss" on:click={() => (err = '')}>dismiss</button>
      </div>
    {/if}

    <div class="list">
      <table>
        <thead>
          <tr><th>Name</th><th class="num">Size</th><th>Modified</th><th class="act">Actions</th></tr>
        </thead>
        <tbody>
          {#if creating}
            <tr class="create-row">
              <td colspan="4">
                <span class="ic" style="color:{creating === 'folder' ? '#89b4fa' : '#7f849c'}">
                  <Icon name={creating === 'folder' ? 'folder' : 'file'} />
                </span>
                <!-- svelte-ignore a11y-autofocus -->
                <input
                  class="inline"
                  placeholder={creating === 'folder' ? 'folder name' : 'file name'}
                  bind:value={newName}
                  on:keydown={(e) => e.key === 'Enter' && submitCreate()}
                  autofocus
                />
                <button class="mini ok" on:click={submitCreate}>Create</button>
                <button class="mini" on:click={() => (creating = '')}>Cancel</button>
              </td>
            </tr>
          {/if}

          {#if cwd}
            <tr class="up" on:click={() => load(parentOf(cwd))}>
              <td colspan="4"><span class="ic"><Icon name="corner-up-left" /></span> ..</td>
            </tr>
          {/if}

          {#each entries as e (e.path)}
            <tr>
              <td class="name-cell">
                <span class="ic" style="color:{e.is_dir ? 'var(--blue)' : 'var(--text-dim)'}">
                  <Icon name={e.is_dir ? 'folder' : 'file'} />
                </span>
                {#if renamingPath === e.path}
                  <!-- svelte-ignore a11y-autofocus -->
                  <input
                    class="inline"
                    bind:value={renameValue}
                    on:keydown={(ev) => { if (ev.key === 'Enter') submitRename(e); if (ev.key === 'Escape') renamingPath = null; }}
                    autofocus
                  />
                  <button class="mini ok" on:click={() => submitRename(e)}>OK</button>
                  <button class="mini" on:click={() => (renamingPath = null)}>✕</button>
                {:else}
                  <button class="link" on:click={() => openEntry(e)}>{e.name}</button>
                {/if}
              </td>
              <td class="num">{e.is_dir ? '—' : fmtSize(e.size)}</td>
              <td class="date">{fmtDate(e.mod_time)}</td>
              <td class="act">
                {#if confirmDelete === e.path}
                  <span class="confirm">delete?</span>
                  <button class="iconbtn ok" title="Confirm delete" on:click={() => doDelete(e)}><Icon name="check" /></button>
                  <button class="iconbtn" title="Cancel" on:click={() => (confirmDelete = null)}><Icon name="x" /></button>
                {:else}
                  {#if !e.is_dir}
                    <button class="iconbtn" title="Open" on:click={() => openFile(e)}><Icon name="code" /></button>
                    <a class="iconbtn" title="Download" href={'/api/files/download?path=' + encodeURIComponent(e.path)}><Icon name="download" /></a>
                  {/if}
                  <button class="iconbtn" title="Rename" on:click={() => startRename(e)}><Icon name="pencil" /></button>
                  <button class="iconbtn danger-hover" title="Delete" on:click={() => (confirmDelete = e.path)}><Icon name="trash" /></button>
                {/if}
              </td>
            </tr>
          {/each}

          {#if !loading && entries.length === 0 && !creating}
            <tr><td colspan="4" class="empty">empty folder</td></tr>
          {/if}
        </tbody>
      </table>
    </div>

    {:else if tab === 'gallery' || tab === 'videos'}
      <div class="grid">
        {#each media as m (m.path)}
          <button class="cell" on:click={() => viewMedia(m)} title={m.name}>
            {#if m.thumb}
              <img class="thumb" loading="lazy" src={thumbUrl(m.path)} alt={m.name} />
            {:else if m.name.toLowerCase().endsWith('.svg')}
              <img class="thumb" loading="lazy" src={rawUrl(m.path)} alt={m.name} />
            {:else}
              <span class="ph"><Icon name={tab === 'videos' ? 'play' : 'file'} size={26} /></span>
            {/if}
            {#if tab === 'videos' && m.thumb}<span class="playover"><Icon name="play" size={18} /></span>{/if}
            <span class="cap">{m.name}</span>
          </button>
        {/each}
        {#if mediaLoading}<p class="empty2">Loading…</p>
        {:else if media.length === 0}<p class="empty2">No {tab === 'videos' ? 'videos' : 'photos'} in your media folder yet.</p>{/if}
      </div>

    {:else}
      <div class="list">
        <table>
          <thead><tr><th>Document</th><th class="num">Size</th><th>Modified</th></tr></thead>
          <tbody>
            {#each media as m (m.path)}
              <tr>
                <td class="name-cell"><span class="ic" style="color:var(--text-dim)"><Icon name="file" /></span><button class="link" on:click={() => editFile(m)}>{m.name}</button></td>
                <td class="num">{fmtSize(m.size)}</td>
                <td class="date">{fmtDate(m.mod_time)}</td>
              </tr>
            {/each}
            {#if !mediaLoading && media.length === 0}<tr><td colspan="3" class="empty">No documents found.</td></tr>{/if}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</div>

<style>
  .files { position: relative; height: var(--page-h); display: flex; flex-direction: column; }

  .mediatabs { display: flex; gap: 0.4rem; margin-bottom: 1rem; flex-shrink: 0; }
  .mediatabs button { background: var(--surface); border: 1px solid var(--border); color: var(--text-muted); font-family: inherit; font-size: 0.85rem; padding: 0.4rem 0.9rem; border-radius: 6px; cursor: pointer; }
  .mediatabs button:hover { border-color: var(--border-3); color: var(--text); }
  .mediatabs button.on { background: var(--surface-3); color: var(--text); border-color: var(--blue); }

  .grid { flex: 1; overflow-y: auto; display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 0.75rem; align-content: start; }
  .cell { position: relative; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; overflow: hidden; cursor: pointer; padding: 0; display: flex; flex-direction: column; aspect-ratio: 1; font-family: inherit; }
  .cell:hover { border-color: var(--border-3); }
  .thumb { width: 100%; flex: 1; min-height: 0; object-fit: cover; background: var(--surface-2); display: block; }
  .ph { flex: 1; display: flex; align-items: center; justify-content: center; color: var(--text-muted); background: var(--surface-2); }
  .playover { position: absolute; top: calc(50% - 0.85rem); left: 50%; transform: translate(-50%, -50%); background: rgba(0, 0, 0, 0.55); color: #fff; border-radius: 50%; width: 36px; height: 36px; display: flex; align-items: center; justify-content: center; pointer-events: none; }
  .cap { font-size: 0.7rem; color: var(--text-2); padding: 0.3rem 0.45rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; background: var(--surface); border-top: 1px solid var(--surface-2); text-align: left; }
  .empty2 { grid-column: 1 / -1; color: var(--text-muted); text-align: center; padding: 2rem; }

  /* media viewer */
  .viewer { position: absolute; inset: 0; display: flex; flex-direction: column; background: var(--bg); border: 1px solid var(--surface-2); border-radius: 6px; overflow: hidden; }
  .vbar { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 0.6rem 1rem; background: var(--surface); border-bottom: 1px solid var(--surface-2); flex-shrink: 0; }
  .vname { color: var(--text); font-weight: 600; font-size: 0.9rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .vactions { display: flex; gap: 0.5rem; flex-shrink: 0; }
  .vbtn { background: var(--surface-2); border: 1px solid var(--border); color: var(--text-2); font-family: inherit; font-size: 0.78rem; padding: 0.35rem 0.8rem; border-radius: 5px; cursor: pointer; text-decoration: none; }
  .vbtn:hover { border-color: var(--border-3); color: var(--text); }
  .vbody { flex: 1; min-height: 0; display: flex; align-items: center; justify-content: center; padding: 1.5rem; overflow: auto; }
  .vbody img { max-width: 100%; max-height: 100%; object-fit: contain; border-radius: 4px; }
  .vbody video { max-width: 100%; max-height: 100%; border-radius: 4px; background: #000; }
  .vbody audio { width: 100%; max-width: 520px; }
  .vbinary { color: var(--text-muted); font-size: 0.9rem; text-align: center; }
  .vbinary a { color: var(--blue); }

  .bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1rem;
    flex-wrap: wrap;
  }
  .crumbs { display: flex; align-items: baseline; gap: 0.25rem; font-size: 0.9rem; }
  /* root crumb doubles as the page title, matching Overview/Docker */
  .title-crumb { background: none; border: none; color: var(--text); cursor: pointer; font-family: inherit; font-size: 1.3rem; font-weight: 600; padding: 0; }
  .crumb { background: none; border: none; color: var(--blue); cursor: pointer; font-family: inherit; font-size: 0.9rem; padding: 0.2rem 0.3rem; }
  .crumb:hover { text-decoration: underline; }
  .slash { color: var(--border-2); }

  .tools { display: flex; gap: 0.5rem; }
  .tools button {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    font-family: inherit;
    font-size: 0.8rem;
    padding: 0.4rem 0.8rem;
    border-radius: 4px;
    border: 1px solid var(--border);
    background: var(--surface);
    color: var(--text);
    cursor: pointer;
  }
  .tools button:hover { border-color: var(--border-3); }
  .tools .ghost { color: var(--text-muted); padding: 0.4rem 0.55rem; }

  .upbar { height: 3px; background: var(--surface-2); border-radius: 2px; margin-bottom: 0.75rem; overflow: hidden; }
  .upbar span { display: block; height: 100%; background: var(--blue); transition: width 0.15s; }

  .err {
    background: var(--red-bg);
    border: 1px solid var(--red-bd);
    color: var(--red);
    padding: 0.6rem 0.9rem;
    border-radius: 5px;
    margin-bottom: 0.75rem;
    font-size: 0.85rem;
  }
  .dismiss {
    background: none;
    border: none;
    color: var(--text-muted);
    font-family: inherit;
    font-size: 0.75rem;
    cursor: pointer;
    text-decoration: underline;
  }

  .list { flex: 1; overflow-y: auto; background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
  table { width: 100%; border-collapse: collapse; }
  th, td { padding: 0.7rem 1rem; text-align: left; border-bottom: 1px solid var(--surface-2); font-size: 0.88rem; }
  th { color: var(--text-muted); font-weight: normal; position: sticky; top: 0; background: var(--surface); z-index: 1; }
  th.num, td.num { text-align: right; width: 7rem; color: var(--text-2); }
  th.act, td.act { width: 16rem; text-align: right; }
  .date { color: var(--text-muted); font-size: 0.8rem; }

  .ic { margin-right: 0.5rem; display: inline-flex; align-items: center; }
  .name-cell { display: flex; align-items: center; }
  .link { background: none; border: none; color: var(--text); cursor: pointer; font-family: inherit; font-size: 0.88rem; padding: 0; }
  .link:hover { color: var(--blue); }
  .up { cursor: pointer; color: var(--text-2); }
  .up:hover { background: var(--surface-2); }
  .empty { color: var(--text-muted); text-align: center; padding: 2rem; }

  .inline {
    background: var(--bg);
    border: 1px solid var(--border-2);
    border-radius: 3px;
    color: var(--text);
    font-family: inherit;
    font-size: 0.85rem;
    padding: 0.25rem 0.5rem;
    margin-right: 0.4rem;
  }
  .mini {
    font-family: inherit;
    font-size: 0.75rem;
    padding: 0.2rem 0.55rem;
    margin-left: 0.25rem;
    border-radius: 3px;
    border: 1px solid var(--border);
    background: var(--surface-2);
    color: var(--text-2);
    cursor: pointer;
    text-decoration: none;
  }
  .mini:hover { border-color: var(--border-3); color: var(--text); }
  .mini.ok { color: var(--green); border-color: var(--green-bd); }
  .mini.danger { color: var(--red); border-color: var(--red-bd); }

  /* icon-only action buttons in the Actions column */
  .iconbtn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    padding: 0.32rem;
    margin-left: 0.15rem;
    border: none;
    background: none;
    color: var(--text-dim);
    border-radius: 4px;
    cursor: pointer;
    text-decoration: none;
    vertical-align: middle;
  }
  .iconbtn:hover { background: var(--surface-2); color: var(--text); }
  .iconbtn.ok { color: var(--green); }
  .iconbtn.danger-hover:hover { color: var(--red); }
  .confirm { color: var(--red); font-size: 0.8rem; margin-right: 0.25rem; }
  .create-row td { display: flex; align-items: center; }

  @media (max-width: 640px) {
    .list { overflow-x: auto; }
    /* hide the Modified column on phones */
    table th:nth-child(3), table td:nth-child(3) { display: none; }
    th.act, td.act { width: auto; }
    .tools { flex-wrap: wrap; }
    .bar { gap: 0.5rem; }
  }
</style>

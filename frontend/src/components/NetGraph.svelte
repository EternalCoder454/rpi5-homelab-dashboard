<script lang="ts">
  import { onMount, afterUpdate } from 'svelte';

  export let data: Array<{ rx: number; tx: number }> = [];

  let wrap: HTMLDivElement;
  let canvas: HTMLCanvasElement;
  let ctx: CanvasRenderingContext2D | null = null;

  // Hover state for the tooltip + guide line.
  let hover = { show: false, x: 0, tipX: 0, rx: 0, tx: 0, ago: 0 };

  // Canvas colors are pulled from the active theme's CSS variables.
  let cBlue = '#89b4fa',
    cRed = '#f38ba8',
    cGrid = '#23232b',
    cText = '#cdd6f4';
  function readColors() {
    const cs = getComputedStyle(document.documentElement);
    cBlue = cs.getPropertyValue('--blue').trim() || cBlue;
    cRed = cs.getPropertyValue('--red').trim() || cRed;
    cGrid = cs.getPropertyValue('--surface-2').trim() || cGrid;
    cText = cs.getPropertyValue('--text').trim() || cText;
  }

  function resize() {
    if (!canvas || !wrap) return;
    const dpr = window.devicePixelRatio || 1;
    const w = wrap.clientWidth,
      h = wrap.clientHeight;
    canvas.width = w * dpr;
    canvas.height = h * dpr;
    canvas.style.width = w + 'px';
    canvas.style.height = h + 'px';
    ctx = canvas.getContext('2d');
    ctx?.setTransform(dpr, 0, 0, dpr, 0, 0);
    draw();
  }

  function px(i: number, w: number) {
    return data.length < 2 ? 0 : (i / (data.length - 1)) * w;
  }
  function py(v: number, max: number, h: number) {
    // leave 4% headroom top and bottom
    return h - (v / max) * h * 0.92 - h * 0.04;
  }

  function draw() {
    if (!ctx || !wrap) return;
    const w = wrap.clientWidth,
      h = wrap.clientHeight;
    ctx.clearRect(0, 0, w, h);

    // Grid
    ctx.strokeStyle = cGrid;
    ctx.lineWidth = 1;
    ctx.beginPath();
    const cols = 6,
      rows = 4;
    for (let i = 1; i < cols; i++) {
      const x = (i / cols) * w;
      ctx.moveTo(x, 0);
      ctx.lineTo(x, h);
    }
    for (let j = 1; j < rows; j++) {
      const y = (j / rows) * h;
      ctx.moveTo(0, y);
      ctx.lineTo(w, y);
    }
    ctx.stroke();

    if (data.length < 2) return;
    const max = Math.max(...data.map((d) => Math.max(d.rx, d.tx)), 10);

    const series = (key: 'rx' | 'tx', color: string) => {
      if (!ctx) return;
      ctx.beginPath();
      data.forEach((p, i) => {
        const x = px(i, w),
          y = py(p[key], max, h);
        i === 0 ? ctx!.moveTo(x, y) : ctx!.lineTo(x, y);
      });
      ctx.strokeStyle = color;
      ctx.lineWidth = 2;
      ctx.stroke();
      // Area fill under the line (same color, low alpha).
      ctx.lineTo(w, h);
      ctx.lineTo(0, h);
      ctx.closePath();
      ctx.globalAlpha = 0.08;
      ctx.fillStyle = color;
      ctx.fill();
      ctx.globalAlpha = 1;
    };
    series('rx', cBlue);
    series('tx', cRed);

    // Hover guide + markers
    if (hover.show) {
      ctx.strokeStyle = cText;
      ctx.globalAlpha = 0.3;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.moveTo(hover.x, 0);
      ctx.lineTo(hover.x, h);
      ctx.stroke();
      ctx.globalAlpha = 1;
      const dot = (v: number, color: string) => {
        ctx!.beginPath();
        ctx!.arc(hover.x, py(v, max, h), 3.5, 0, Math.PI * 2);
        ctx!.fillStyle = color;
        ctx!.fill();
      };
      dot(hover.rx, cBlue);
      dot(hover.tx, cRed);
    }
  }

  function onMove(e: MouseEvent) {
    if (data.length < 2) {
      return;
    }
    const rect = canvas.getBoundingClientRect();
    const w = wrap.clientWidth;
    const mx = e.clientX - rect.left;
    const i = Math.max(0, Math.min(data.length - 1, Math.round((mx / w) * (data.length - 1))));
    const p = data[i];
    const x = px(i, w);
    hover = {
      show: true,
      x,
      tipX: Math.max(58, Math.min(w - 58, x)),
      rx: p.rx,
      tx: p.tx,
      ago: (data.length - 1 - i) * 2,
    };
    draw();
  }
  function onLeave() {
    hover.show = false;
    draw();
  }

  onMount(() => {
    readColors();
    resize();
    const ro = new ResizeObserver(resize);
    ro.observe(wrap);
    // Recolor when the theme changes.
    const to = new MutationObserver(() => {
      readColors();
      draw();
    });
    to.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    return () => {
      ro.disconnect();
      to.disconnect();
    };
  });

  // Redraw straight to the 2D context on each data update — no reactive DOM.
  afterUpdate(draw);

  $: latest = data.length ? data[data.length - 1] : { rx: 0, tx: 0 };
</script>

<div class="graph" bind:this={wrap}>
  <canvas bind:this={canvas} on:mousemove={onMove} on:mouseleave={onLeave}></canvas>

  <div class="legend">
    <span><i style="background:var(--blue)"></i>RX {latest.rx.toFixed(1)} kbps</span>
    <span><i style="background:var(--red)"></i>TX {latest.tx.toFixed(1)} kbps</span>
  </div>

  {#if hover.show}
    <div class="tip" style="left:{hover.tipX}px">
      <div class="t-time">{hover.ago}s ago</div>
      <div><i style="background:var(--blue)"></i>RX {hover.rx.toFixed(1)} kbps</div>
      <div><i style="background:var(--red)"></i>TX {hover.tx.toFixed(1)} kbps</div>
    </div>
  {/if}
</div>

<style>
  .graph {
    position: relative;
    width: 100%;
    height: 250px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    overflow: hidden;
  }
  canvas { display: block; width: 100%; height: 100%; cursor: crosshair; }
  .legend {
    position: absolute;
    top: 8px;
    left: 12px;
    display: flex;
    gap: 1rem;
    font-size: 0.75rem;
    color: var(--text-2);
    pointer-events: none;
  }
  .legend i,
  .tip i {
    display: inline-block;
    width: 8px;
    height: 8px;
    border-radius: 2px;
    margin-right: 5px;
  }
  .tip {
    position: absolute;
    top: 8px;
    transform: translateX(-50%);
    background: var(--surface-3);
    border: 1px solid var(--border-2);
    border-radius: 5px;
    padding: 6px 9px;
    font-size: 0.72rem;
    color: var(--text);
    pointer-events: none;
    white-space: nowrap;
  }
  .tip .t-time { color: var(--text-muted); margin-bottom: 3px; }
</style>

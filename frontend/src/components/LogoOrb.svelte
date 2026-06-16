<script lang="ts">
  import { onMount, onDestroy } from 'svelte';

  // The Assistant's animated mark: a glowing nucleus that drifts and morphs
  // gently when idle, then swells and brightens with faster ripples while the
  // model is generating. This is a canvas port of the Cairo-drawn orb from the
  // desktop app (Atlas Monitor's logo.go) — the same maths, on a frame clock.
  export let size = 120;
  export let active = false; // true while generating

  let canvas: HTMLCanvasElement;
  let raf = 0;
  let start = performance.now();
  let act = 0; // eased 0 (idle) .. 1 (generating)

  // Orb colours, pulled from the active theme's --mauve so it fits every palette.
  // A light core is mixed toward white. Re-read when the theme attribute changes.
  let orb = { r: 0.62, g: 0.34, b: 0.9 };
  let core = { r: 0.86, g: 0.74, b: 1.0 };
  let observer: MutationObserver | null = null;

  function readColor() {
    const css = getComputedStyle(document.documentElement).getPropertyValue('--mauve').trim();
    const m = css.match(/^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i);
    if (m) {
      orb = { r: parseInt(m[1], 16) / 255, g: parseInt(m[2], 16) / 255, b: parseInt(m[3], 16) / 255 };
      core = { r: orb.r + (1 - orb.r) * 0.55, g: orb.g + (1 - orb.g) * 0.55, b: orb.b + (1 - orb.b) * 0.55 };
    }
  }

  function rgba(c: { r: number; g: number; b: number }, a: number) {
    return `rgba(${Math.round(c.r * 255)},${Math.round(c.g * 255)},${Math.round(c.b * 255)},${a})`;
  }

  function frame(now: number) {
    const dt = Math.min(0.05, (now - lastTick) / 1000);
    lastTick = now;
    const target = active ? 1 : 0;
    act += (target - act) * Math.min(1, dt * 6); // ease ~0.2s, matches the desktop app
    draw(now);
    raf = requestAnimationFrame(frame);
  }
  let lastTick = performance.now();

  function draw(now: number) {
    const ctx = canvas.getContext('2d');
    if (!ctx) return;
    const dpr = window.devicePixelRatio || 1;
    const w = size,
      h = size;
    if (canvas.width !== w * dpr) {
      canvas.width = w * dpr;
      canvas.height = h * dpr;
    }
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, w, h);

    const s = Math.min(w, h);
    const cx = w / 2,
      cy = h / 2;
    const t = (now - start) / 1000;
    const a = act; // 0 idle .. 1 generating

    const R = s * 0.34; // outer ring radius
    const coreR = s * 0.16; // base nucleus radius

    // Halo: concentric translucent fills fading outward.
    const haloN = 9;
    for (let i = haloN; i >= 1; i--) {
      const f = i / haloN;
      ctx.fillStyle = rgba(orb, (0.05 + 0.06 * a) * (1 - f * 0.85));
      ctx.beginPath();
      ctx.arc(cx, cy, coreR + (R * 1.15 - coreR) * f, 0, 2 * Math.PI);
      ctx.fill();
    }

    // Ripples: rings expanding out and fading (faster/brighter when generating).
    const nRip = 3;
    const speed = 0.16 + 0.3 * a;
    ctx.lineWidth = s * 0.012;
    for (let k = 0; k < nRip; k++) {
      const frac = (t * speed + k / nRip) % 1;
      ctx.strokeStyle = rgba(orb, (1 - frac) * (0.16 + 0.2 * a));
      ctx.beginPath();
      ctx.arc(cx, cy, R * (0.85 + 0.45 * frac), 0, 2 * Math.PI);
      ctx.stroke();
    }

    // Outer ring, breathing slightly.
    ctx.strokeStyle = rgba(orb, 0.5 + 0.32 * a);
    ctx.lineWidth = s * 0.022;
    ctx.beginPath();
    ctx.arc(cx, cy, R * (1 + 0.015 * Math.sin(t * 1.1)), 0, 2 * Math.PI);
    ctx.stroke();

    // Nucleus: a wobbling blob — subtle when idle, lively while generating.
    const amp = 0.1 + 0.22 * a;
    const pulse = 1 + (0.04 + 0.1 * a) * Math.sin(t * (1.6 + 1.0 * a));
    const pts = 40;
    ctx.beginPath();
    for (let i = 0; i <= pts; i++) {
      const ang = (2 * Math.PI * i) / pts;
      const wob =
        0.55 * Math.sin(3 * ang + t * 1.3) +
        0.3 * Math.sin(5 * ang - t * 0.9) +
        0.15 * Math.sin(2 * ang + t * 1.9);
      const rr = coreR * pulse * (1 + amp * wob * 0.5);
      const x = cx + rr * Math.cos(ang),
        y = cy + rr * Math.sin(ang);
      if (i === 0) ctx.moveTo(x, y);
      else ctx.lineTo(x, y);
    }
    ctx.closePath();
    ctx.fillStyle = rgba(core, 0.45 + 0.2 * a);
    ctx.fill();

    // Bright inner glow that drifts a touch.
    const hx = cx + coreR * 0.12 * Math.cos(t * 0.8);
    const hy = cy + coreR * 0.12 * Math.sin(t * 1.1);
    for (let i = 3; i >= 1; i--) {
      const f = i / 3;
      ctx.fillStyle = `rgba(255,255,255,${(0.12 + 0.18 * a) * (1 - 0.55 * f)})`;
      ctx.beginPath();
      ctx.arc(hx, hy, coreR * (0.35 + 0.5 * f), 0, 2 * Math.PI);
      ctx.fill();
    }
    ctx.fillStyle = 'rgba(255,255,255,0.92)';
    ctx.beginPath();
    ctx.arc(hx, hy, coreR * 0.32, 0, 2 * Math.PI);
    ctx.fill();
  }

  onMount(() => {
    readColor();
    observer = new MutationObserver(readColor);
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    raf = requestAnimationFrame(frame);
  });
  onDestroy(() => {
    cancelAnimationFrame(raf);
    observer?.disconnect();
  });
</script>

<canvas bind:this={canvas} style="width:{size}px;height:{size}px"></canvas>

<style>
  canvas {
    display: block;
  }
</style>

import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

// vitePreprocess enables <script lang="ts"> inside .svelte files. Without this
// the TypeScript in every component fails to compile during `vite build`.
export default {
  preprocess: vitePreprocess(),
};

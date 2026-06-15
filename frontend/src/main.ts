// Self-hosted JetBrains Mono (latin subset) so the dashboard looks identical
// on every device without relying on a locally-installed font.
import '@fontsource/jetbrains-mono/latin-400.css';
import '@fontsource/jetbrains-mono/latin-500.css';
import '@fontsource/jetbrains-mono/latin-600.css';
import '@fontsource/jetbrains-mono/latin-700.css';

// Cousine — a compact, clean Consolas-style monospace used for the terminal.
import '@fontsource/cousine/latin-400.css';
import '@fontsource/cousine/latin-700.css';

import App from './App.svelte';

const app = new App({
  target: document.getElementById('app')!,
});

export default app;

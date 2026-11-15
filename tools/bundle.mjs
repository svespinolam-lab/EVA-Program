// EVA: bundle local styles, scripts, and the PNG logo into one HTML file.
// Usage: node tools/bundle.mjs
// Edit source files; dist/eva-app.html is generated.
import { readFile, writeFile, mkdir } from 'node:fs/promises';

const root = new URL('../', import.meta.url);
const entry = new URL('web/app/index.html', root);
const local = (path) => new URL(path, entry);
const isRemote = (path) => /^(?:[a-z][a-z\d+.-]*:|\/\/)/i.test(path);

async function replaceAsync(text, pattern, replace) {
  const parts = [];
  let last = 0;
  for (const match of text.matchAll(pattern)) {
    parts.push(text.slice(last, match.index), await replace(...match));
    last = match.index + match[0].length;
  }
  parts.push(text.slice(last));
  return parts.join('');
}

let html = await readFile(entry, 'utf8');

// Resolve app and shared styles from the entry point. Missing files fail the build.
html = await replaceAsync(html, /<link rel="stylesheet" href="([^"]+)">/g, async (tag, href) => {
  if (isRemote(href)) return tag;
  const css = await readFile(local(href), 'utf8');
  return `<style>/* ${href} */\n${css}\n</style>`;
});

// Preserve classic-script order. All scripts follow the app markup.
html = await replaceAsync(html, /<script src="([^"]+)"(?: defer)?><\/script>/g, async (tag, src) => {
  if (isRemote(src)) return tag;
  const js = (await readFile(local(src), 'utf8')).replace(/<\/script/gi, '<\\/script');
  return `<script>/* ${src} */\n${js}\n</script>`;
});

// Embed the favicon, splash image, and config.app.logo as the same PNG data URI.
const logo = await readFile(local('../shared/logo.png'));
const logoData = 'data:image/png;base64,' + logo.toString('base64');
html = html.replace(/(["'])\.\.\/shared\/logo\.png\1/g, (_, quote) => quote + logoData + quote);

await mkdir(new URL('dist/', root), { recursive: true });
await writeFile(new URL('dist/eva-app.html', root), html, 'utf8');
console.log(`dist/eva-app.html ready (${Math.round(Buffer.byteLength(html) / 1024)} KB)`);
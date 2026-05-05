import { build } from 'esbuild'

const entryPoints = {
  feed: 'static/js/src/feed/index.js',
  shorts: 'static/js/src/shorts/index.js',
  player: 'static/js/src/player/index.js',
}

await build({
  entryPoints,
  bundle: true,
  outdir: 'static/js/dist',
  format: 'iife',
  minify: false,
  sourcemap: true,
  logLevel: 'info',
})

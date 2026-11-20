import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { defineConfig } from 'tsdown'

const raw = {
  name: 'raw',
  resolveId: (source: string, importer?: string) => source.endsWith('?raw') && importer ? resolve(dirname(importer), source) : undefined,
  load: (id: string) => id.endsWith('?raw')
    ? { code: `export default ${JSON.stringify(readFileSync(id.slice(0, -4), 'utf8'))}`, moduleType: 'js' as const }
    : undefined,
}

export default defineConfig([
  {
    entry: ['src/index.ts'], format: ['esm'], dts: true, sourcemap: false, clean: true,
    outDir: 'lib', platform: 'node', external: [/^@deepseek-ai\//],
  },
  {
    entry: { client: 'src/client/index.tsx' }, format: ['cjs'], dts: false, sourcemap: false,
    clean: false, outDir: 'lib', platform: 'browser', plugins: [raw],
    deps: { neverBundle: ['react', 'react/jsx-runtime', 'react-dom'] },
    outputOptions: {
      entryFileNames: 'client.js',
      banner: 'window.__ModuleLoader__.load({ id: "hgdb_monitor", factory: (require) => {',
      footer: 'return module.exports; } });',
      intro: 'var module = { exports: {} }; var exports = module.exports;',
    },
  },
])

import { defineConfig } from 'tsdown'
export default defineConfig({
  entry: { preview: 'src/client/preview.tsx' }, outDir: 'dev', format: 'iife', platform: 'browser', dts: false, clean: false,
  noExternal: [/^react(?:-dom)?(?:\/.*)?$/],
})

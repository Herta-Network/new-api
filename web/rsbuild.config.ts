import { createHash } from 'node:crypto'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import {
  defineConfig,
  ensureAssetPrefix,
  loadEnv,
  type HtmlBasicTag,
  type ModifyHTMLTagsContext,
  type RsbuildPlugin,
} from '@rsbuild/core'
import { pluginReact } from '@rsbuild/plugin-react'
import { pluginTailwindcss } from '@rsbuild/plugin-tailwindcss'
import { tanstackRouter } from '@tanstack/router-plugin/rspack'

import { INTERFACE_LANGUAGE_OPTIONS } from './src/i18n/languages.ts'
import { localePreloadScript } from './src/i18n/locale-preload.ts'
import { themeBootstrapScript } from './src/lib/theme-bootstrap.ts'
import { THEME_STORAGE_KEYS } from './src/lib/theme-storage-keys.ts'

const __dirname = path.dirname(fileURLToPath(import.meta.url))

// Emits a `<head>` script as `static/js/<name>.<hash>.js` and returns the tag
// that loads it. index.html carries no inline script, so the console's
// Content-Security-Policy allows scripts from its own origin without hashes
// that would change with every build.
function headScriptTag(
  context: ModifyHTMLTagsContext,
  name: string,
  source: string
): HtmlBasicTag {
  const hash = createHash('sha256').update(source).digest('hex').slice(0, 10)
  const file = `static/js/${name}.${hash}.js`
  if (!context.compilation.getAsset(file)) {
    context.compilation.emitAsset(
      file,
      new context.compiler.rspack.sources.RawSource(source)
    )
  }
  return {
    tag: 'script',
    attrs: { src: ensureAssetPrefix(file, context.assetPrefix) },
  }
}

// Loads the theme script from `src/lib/theme-bootstrap.ts` first in <head>,
// before the bundle and the stylesheet, so the theme class is set before the
// first paint.
const pluginThemeBootstrap: RsbuildPlugin = {
  name: 'newapi:theme-bootstrap',
  setup(api) {
    api.modifyHTMLTags((tags, context) => {
      tags.headTags.unshift(
        headScriptTag(
          context,
          'theme-bootstrap',
          themeBootstrapScript(THEME_STORAGE_KEYS.mode)
        )
      )
      return tags
    })
  },
}

// Loads the script from `src/i18n/locale-preload.ts` in index.html, with
// the hashed URL of every `locale-<code>` chunk, so the visitor's locale
// downloads alongside the main bundle instead of after it.
const pluginLocalePreload: RsbuildPlugin = {
  name: 'newapi:locale-preload',
  setup(api) {
    api.modifyHTMLTags((tags, context) => {
      const chunkUrls = Object.fromEntries(
        INTERFACE_LANGUAGE_OPTIONS.map(({ code }) => {
          const chunk = context.compilation.namedChunks.get(`locale-${code}`)
          const file =
            chunk && [...chunk.files].find((name) => name.endsWith('.js'))
          if (!file) {
            throw new Error(
              `No "locale-${code}" chunk to preload: keep its webpackChunkName in src/i18n/locale-loader.ts`
            )
          }
          return [code, ensureAssetPrefix(file, context.assetPrefix)]
        })
      )
      // After the bundle's scripts, so they are requested first, and before
      // the stylesheet: a parser-inserted script after a pending stylesheet
      // waits for it to load.
      const stylesheet = tags.headTags.findIndex(
        (tag) => tag.attrs?.rel === 'stylesheet'
      )
      tags.headTags.splice(
        stylesheet === -1 ? tags.headTags.length : stylesheet,
        0,
        headScriptTag(context, 'locale-preload', localePreloadScript(chunkUrls))
      )
      return tags
    })
  },
}

export default defineConfig(({ envMode }) => {
  const env = loadEnv({ mode: envMode, prefixes: ['VITE_'] })
  const serverUrl =
    process.env.VITE_REACT_APP_SERVER_URL ||
    env.rawPublicVars.VITE_REACT_APP_SERVER_URL ||
    'http://localhost:3000'

  const isProd = envMode === 'production'
  const devProxy = Object.fromEntries(
    (['/api', '/v1', '/mj', '/pg'] as const).map((key) => [
      key,
      { target: serverUrl, changeOrigin: true },
    ])
  ) as Record<string, { target: string; changeOrigin: boolean }>

  return {
    plugins: [
      pluginReact(),
      pluginTailwindcss({ optimize: false }),
      pluginThemeBootstrap,
      // Dev compiles dynamic imports lazily, so the locale chunks do not exist
      // yet when the dev server writes index.html.
      isProd && pluginLocalePreload,
    ],
    // Rsbuild 2: replaces deprecated `performance.chunkSplit` (RSPack 2 aligned)
    splitChunks: {
      preset: 'default',
      cacheGroups: {
        'vendor-react': {
          test: /node_modules[\\/](react|react-dom)[\\/]/,
          name: 'vendor-react',
          chunks: 'all',
          priority: 0,
          enforce: true,
        },
        'vendor-ui-primitives': {
          test: /node_modules[\\/](@base-ui|@radix-ui)[\\/]/,
          name: 'vendor-ui-primitives',
          chunks: 'all',
          priority: 0,
          enforce: true,
        },
        'vendor-tanstack': {
          test: /node_modules[\\/]@tanstack[\\/]/,
          name: 'vendor-tanstack',
          chunks: 'all',
          priority: 0,
          enforce: true,
        },
      },
    },
    source: {
      entry: {
        index: './src/main.tsx',
      },
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },
    html: {
      template: './index.html',
      favicon: './public/favicon.ico',
    },
    server: {
      host: '0.0.0.0',
      strictPort: false,
      proxy: devProxy,
    },
    output: {
      // Production optimizations
      minify: isProd,
      target: 'web',
      distPath: {
        root: 'dist',
      },
      // Rely on Rsbuild default legalComments ("linked" → per-chunk *.LICENSE.txt) in all modes.
      // Do not set "none" in production: that strips minifier-preserved third-party notices and
      // extracted license files, which some distributions require for open-source compliance.
    },
    performance: {
      // Remove console in production
      removeConsole: isProd ? ['log'] : false,
      buildCache: false,
    },
    tools: {
      rspack: {
        plugins: [
          tanstackRouter({
            target: 'react',
            // Dev: avoid per-route async chunks (reduces white flash on navigation + faster HMR feedback).
            // Prod: keep route-based code splitting.
            autoCodeSplitting: isProd,
          }),
        ],
      },
    },
  }
})

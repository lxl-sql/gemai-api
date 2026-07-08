import path from 'path'
import { createRequire } from 'module'
import { fileURLToPath } from 'url'
import { defineConfig, loadEnv } from '@rsbuild/core'
import { pluginReact } from '@rsbuild/plugin-react'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const require = createRequire(import.meta.url)
const semiUiDir = path.resolve(
  path.dirname(require.resolve('@douyinfe/semi-ui')),
  '../..',
)
// semi-ui 依赖 date-fns v2 生态；workspace 根可能被提升为 date-fns v4，
// 显式把 classic 的 date-fns/date-fns-tz 解析固定到本包声明的 v2 版本。
const dateFnsDir = path.dirname(require.resolve('date-fns/package.json'))
const dateFnsTzDir = path.dirname(require.resolve('date-fns-tz/package.json'))

// classic 使用 vchart 1.8 系，而 workspace 根被 default 的 vchart 2.x 生态占据，
// 导致 vrender/vutils 等在 node_modules 中存在多份物理副本。vrender-core 的
// vglobal 是模块级单例，多副本会被 rspack 打包成多个实例，env 注册与 canvas
// 分配落在不同实例上，运行时报 "Cannot read properties of undefined (reading 'createCanvas')"。
// 这里以 classic 自己的 @visactor/vchart 为锚点，把整个 1.8 依赖链钉到同一份副本。
const vchartPkgJson = require.resolve('@visactor/vchart/package.json')
const vchartRequire = createRequire(vchartPkgJson)
// 部分 @visactor 包的 exports 未暴露 ./package.json，
// 只能 resolve 入口文件后截取 node_modules/<pkg> 包根目录。
const resolvePkgDir = (pkg: string) => {
  const entry = vchartRequire.resolve(pkg)
  const marker = path.join('node_modules', ...pkg.split('/')) + path.sep
  const idx = entry.lastIndexOf(marker)
  if (idx === -1) {
    throw new Error(`cannot locate package dir for ${pkg} from ${entry}`)
  }
  return entry.slice(0, idx + marker.length - 1)
}
const visactorAlias = Object.fromEntries(
  [
    '@visactor/vchart',
    '@visactor/react-vchart',
    '@visactor/vrender-core',
    '@visactor/vrender-kits',
    '@visactor/vrender-components',
    '@visactor/vutils',
    '@visactor/vutils-extension',
    '@visactor/vscale',
    '@visactor/vdataset',
  ].map((pkg) => [pkg, resolvePkgDir(pkg)]),
)

export default defineConfig(({ envMode }) => {
  const env = loadEnv({ mode: envMode, prefixes: ['VITE_'] })
  const clientServerUrl =
    process.env.VITE_REACT_APP_SERVER_URL ||
    env.rawPublicVars.VITE_REACT_APP_SERVER_URL ||
    ''
  const proxyServerUrl =
    clientServerUrl ||
    'http://localhost:3000'
  const isProd = envMode === 'production'
  const devProxy = Object.fromEntries(
    (['/api', '/mj', '/pg'] as const).map((key) => [
      key,
      { target: proxyServerUrl, changeOrigin: true },
    ]),
  ) as Record<string, { target: string; changeOrigin: boolean }>

  return {
    plugins: [pluginReact()],
    source: {
      entry: {
        index: './src/index.jsx',
      },
      define: {
        'import.meta.env.VITE_REACT_APP_SERVER_URL': JSON.stringify(
          clientServerUrl,
        ),
      },
    },
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
        '@douyinfe/semi-ui/dist/css/semi.css': path.resolve(
          semiUiDir,
          'dist/css/semi.css',
        ),
        'date-fns': dateFnsDir,
        'date-fns-tz': dateFnsTzDir,
        ...visactorAlias,
      },
    },
    html: {
      template: './index.html',
    },
    server: {
      host: '0.0.0.0',
      strictPort: false,
      proxy: devProxy,
    },
    output: {
      minify: isProd,
      target: 'web',
      distPath: {
        root: 'dist',
      },
    },
    performance: {
      removeConsole: isProd ? ['log'] : false,
      buildCache: {
        cacheDigest: [process.env.VITE_REACT_APP_VERSION],
      },
    },
    tools: {
      rspack: {
        module: {
          rules: [
            {
              test: /src[\\/].*\.js$/,
              type: 'javascript/auto',
              use: [
                {
                  loader: 'builtin:swc-loader',
                  options: {
                    jsc: {
                      parser: {
                        syntax: 'ecmascript',
                        jsx: true,
                      },
                      transform: {
                        react: {
                          runtime: 'automatic',
                          development: !isProd,
                          refresh: !isProd,
                        },
                      },
                    },
                  },
                },
              ],
            },
          ],
        },
      },
    },
  }
})

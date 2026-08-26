import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

// 打包产物直接落到 web/assets/dist，由 web.go 的 //go:embed all:assets 一起塞进二进制。
//
// 文件名固定为 xui.js / xui.css 而不带内容哈希：Go 模板要静态引用它们，
// 引入 manifest 解析只是为了一个永远只有两个条目的映射表，不划算。
// 缓存靠 URL 上的 ?v=<面板版本号> 打破（见 web/html/common/head.html）。
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    outDir: '../assets/dist',
    emptyOutDir: true,
    // 面板跑在自有服务器上，source map 会把整份源码暴露给任何访客。
    sourcemap: false,
    target: 'es2020',
    rollupOptions: {
      input: fileURLToPath(new URL('./src/main.ts', import.meta.url)),
      output: {
        format: 'es',
        entryFileNames: 'xui.js',
        chunkFileNames: 'xui-[name].js',
        assetFileNames: 'xui.[ext]',
        // 单文件产物：面板是内网/自建部署，少一次请求比精细分包更有价值，
        // 也让 Go 模板只需要引用一个 <script>。
        inlineDynamicImports: true,
      },
    },
  },
})

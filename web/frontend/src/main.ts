/**
 * main.ts —— 前端入口。
 *
 * 面板保持"多页 + 每页一个 Vue 应用"的形态，而不是引 vue-router 做单页：
 * 后端的 basePath 可以是任意前缀，路由前缀要在两处保持同步；而这四个页面
 * 之间本来就没有共享状态，整页跳转的代价可以忽略。
 *
 * 页面身份由后端注入（window.__XUI__.page），不靠解析 location.pathname —— 
 * 后者在自定义 basePath 下就会认错。
 */
import Antd, { ConfigProvider } from 'ant-design-vue'
import { createApp } from 'vue'

import 'ant-design-vue/dist/reset.css'

import App from './App.vue'
import { boot } from './boot'
// 副作用 import：把每种协议的 defaults() 注册进 core.ts 的表。
import './models/protocols'
import './style.css'
import { glassTheme } from './theme'

// Modal.confirm() / message() 这类静态调用自己挂一个渲染根，拿不到 App.vue 里
// 那个 <a-config-provider>。它们读的是这份全局配置——不设，删除确认框就会在
// 一片暗色玻璃中间弹出一块纯白板。
//
// 断言的原因：config() 的 .d.ts 还写着 4.x 之前那个 { primaryColor, ... } 的
// Theme，但实现是把整个 params 原样 spread 到 <ConfigProvider>（见
// es/modal/confirm.js 的 Wrapper），而组件的 theme prop 收的正是 ThemeConfig。
// 只有类型声明落后，运行时是通的。
ConfigProvider.config({ theme: glassTheme } as unknown as Parameters<typeof ConfigProvider.config>[0])

const app = createApp(App)
app.use(Antd)
app.config.globalProperties.$boot = boot
app.mount('#app')

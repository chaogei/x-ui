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
import Antd from 'ant-design-vue'
import { createApp } from 'vue'

import 'ant-design-vue/dist/reset.css'

import App from './App.vue'
import { boot } from './boot'
// 副作用 import：把每种协议的 defaults() 注册进 core.ts 的表。
import './models/protocols'
import './style.css'

const app = createApp(App)
app.use(Antd)
app.config.globalProperties.$boot = boot
app.mount('#app')

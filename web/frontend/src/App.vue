<script setup lang="ts">
/**
 * App —— 按后端注入的页面身份挂载对应视图。
 *
 * 整棵树包在 a-config-provider 里：暗色玻璃主题必须在 antd 组件渲染之前就位，
 * 否则卡片、表格、弹窗会先刷一层不透明白，磨砂层背后就什么都没有了。
 * 脱离组件树的静态调用（Modal.confirm / message）走 main.ts 里的全局配置。
 */
import { computed } from 'vue'

import AppShell from './components/AppShell.vue'
import { boot } from './boot'
import { glassTheme } from './theme'
import InboundsView from './views/InboundsView.vue'
import LoginView from './views/LoginView.vue'
import SettingView from './views/SettingView.vue'
import StatusView from './views/StatusView.vue'

const page = computed(() => boot.page)
</script>

<template>
  <a-config-provider :theme="glassTheme">
    <LoginView v-if="page === 'login'" />
    <AppShell v-else>
      <StatusView v-if="page === 'status'" />
      <InboundsView v-else-if="page === 'inbounds'" />
      <SettingView v-else-if="page === 'setting'" />
    </AppShell>
  </a-config-provider>
</template>

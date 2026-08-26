<script setup lang="ts">
/**
 * AppShell —— 登录后各页共用的侧边栏 + 内容区。
 *
 * 语言切换写 `lang` cookie 后整页刷新：词典由后端注入，换语言必须让服务端
 * 重新渲染一次，不存在"前端热切"这条路（也就不会出现前后端语言不一致）。
 */
import {
  DashboardOutlined,
  GithubOutlined,
  GlobalOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  SettingOutlined,
  UserOutlined,
} from '@ant-design/icons-vue'
import { computed, ref } from 'vue'

import { boot, panelUrl, setLang, t } from '../boot'

const collapsed = ref(false)
const drawerOpen = ref(false)

const selectedKeys = computed(() => [boot.requestUri])

const LANG_PREFIX = 'lang:'

function handleClick(key: string): void {
  drawerOpen.value = false
  if (key.startsWith(LANG_PREFIX)) {
    setLang(key.slice(LANG_PREFIX.length))
  } else if (key.startsWith('http')) {
    window.open(key, '_blank', 'noopener')
  } else {
    location.href = key
  }
}
</script>

<template>
  <a-layout style="min-height: 100%">
    <a-layout-sider v-model:collapsed="collapsed" collapsible breakpoint="md" :collapsed-width="0">
      <a-menu theme="dark" mode="inline" :selected-keys="selectedKeys" @click="(info: any) => handleClick(String(info.key))">
        <a-menu-item :key="panelUrl('xui/')">
          <template #icon><DashboardOutlined /></template>
          <span>{{ t('menu_system_status') }}</span>
        </a-menu-item>
        <a-menu-item :key="panelUrl('xui/inbounds')">
          <template #icon><UserOutlined /></template>
          <span>{{ t('menu_inbound_list') }}</span>
        </a-menu-item>
        <a-menu-item :key="panelUrl('xui/setting')">
          <template #icon><SettingOutlined /></template>
          <span>{{ t('menu_panel_setting') }}</span>
        </a-menu-item>
        <a-sub-menu key="others">
          <template #icon><GithubOutlined /></template>
          <template #title>{{ t('menu_others') }}</template>
          <a-menu-item key="https://github.com/chaogei/x-ui/">Github</a-menu-item>
        </a-sub-menu>
        <a-sub-menu key="language">
          <template #icon><GlobalOutlined /></template>
          <template #title>{{ t('language') }}</template>
          <a-menu-item :key="LANG_PREFIX">{{ t('lang_follow_browser') }}</a-menu-item>
          <a-menu-item v-for="lang in boot.languages" :key="LANG_PREFIX + lang.Code">{{ lang.Name }}</a-menu-item>
        </a-sub-menu>
        <a-menu-item :key="panelUrl('logout')">
          <template #icon><LogoutOutlined /></template>
          <span>{{ t('logout') }}</span>
        </a-menu-item>
      </a-menu>
    </a-layout-sider>

    <a-layout>
      <a-layout-content class="xui-content">
        <a-button class="xui-drawer-handle" type="text" @click="drawerOpen = true">
          <MenuFoldOutlined />
        </a-button>
        <slot />
      </a-layout-content>
    </a-layout>

    <a-drawer v-model:open="drawerOpen" placement="left" :closable="false" :body-style="{ padding: 0 }" width="220">
      <a-menu theme="light" mode="inline" :selected-keys="selectedKeys" @click="(info: any) => handleClick(String(info.key))">
        <a-menu-item :key="panelUrl('xui/')">{{ t('menu_system_status') }}</a-menu-item>
        <a-menu-item :key="panelUrl('xui/inbounds')">{{ t('menu_inbound_list') }}</a-menu-item>
        <a-menu-item :key="panelUrl('xui/setting')">{{ t('menu_panel_setting') }}</a-menu-item>
        <a-menu-item :key="panelUrl('logout')">{{ t('logout') }}</a-menu-item>
      </a-menu>
    </a-drawer>
  </a-layout>
</template>

<style scoped>
/* 侧边栏在窄屏被折叠成 0 宽，这个按钮是唯一的入口。 */
.xui-drawer-handle {
  display: none;
  margin-bottom: 8px;
}

@media (max-width: 768px) {
  .xui-drawer-handle {
    display: inline-block;
  }
}
</style>

<script setup lang="ts">
/**
 * AppShell —— 登录后各页共用的侧边栏 + 顶栏 + 内容区。
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
  MenuUnfoldOutlined,
  SettingOutlined,
  UserOutlined,
} from '@ant-design/icons-vue'
import { computed, ref } from 'vue'

import { boot, panelUrl, setLang, t } from '../boot'

const collapsed = ref(false)

const selectedKeys = computed(() => [boot.requestUri])

/** 顶栏标题跟着后端注入的页面身份走，不去解析 location。 */
const pageTitle = computed(() => {
  switch (boot.page) {
    case 'inbounds':
      return t('menu_inbound_list')
    case 'setting':
      return t('menu_panel_setting')
    default:
      return t('menu_system_status')
  }
})

const LANG_PREFIX = 'lang:'

function handleClick(key: string): void {
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
  <a-layout class="xui-shell">
    <!--
      breakpoint="md" + collapsed-width="0"：窄屏自动收起。菜单只此一份，
      语言切换在手机上同样可达。展开/收起统一由顶栏那个按钮控制，antd 自带的
      浮动把手已在 style.css 里收掉——它绝对定位在内容区左上角，会遮住页面。
    -->
    <a-layout-sider
      v-model:collapsed="collapsed"
      class="xui-sider"
      collapsible
      breakpoint="md"
      :collapsed-width="0"
      :width="240"
    >
      <div class="xui-sider__inner">
        <a class="xui-brand" :href="panelUrl('xui/')">
          <span class="xui-brand__mark" aria-hidden="true">x</span>
          <span class="xui-brand__text">
            <span class="xui-brand__name">x-ui</span>
            <span class="xui-brand__version">{{ boot.version }}</span>
          </span>
        </a>

        <a-menu
          class="xui-nav"
          mode="inline"
          :selected-keys="selectedKeys"
          @click="(info: any) => handleClick(String(info.key))"
        >
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
      </div>
    </a-layout-sider>

    <!-- 窄屏侧边栏浮在内容之上，点遮罩收回去。宽屏由 CSS 隐藏。 -->
    <div v-if="!collapsed" class="xui-scrim" @click="collapsed = true" />

    <a-layout class="xui-main">
      <header class="xui-topbar">
        <button
          class="xui-topbar__toggle"
          type="button"
          :aria-label="t('menu_toggle')"
          :title="t('menu_toggle')"
          @click="collapsed = !collapsed"
        >
          <MenuUnfoldOutlined v-if="collapsed" />
          <MenuFoldOutlined v-else />
        </button>
        <h1 class="xui-topbar__title">{{ pageTitle }}</h1>
        <span class="xui-topbar__spacer" />
        <span class="xui-topbar__version">x-ui {{ boot.version }}</span>
      </header>

      <a-layout-content class="xui-content">
        <slot />
      </a-layout-content>
    </a-layout>
  </a-layout>
</template>

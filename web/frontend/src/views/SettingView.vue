<script setup lang="ts">
/**
 * SettingView —— 面板设置。
 */
import { Modal } from 'ant-design-vue'
import { computed, onMounted, ref } from 'vue'

import SettingListItem from '../components/SettingListItem.vue'
import TwoFactorCard from '../components/TwoFactorCard.vue'
import { t } from '../boot'
import { post, sleep } from '../http'
import { defaultAllSetting, fromServer, settingsEqual, type AllSetting } from '../models/setting'

const spinning = ref(false)
const saved = ref<AllSetting>(defaultAllSetting())
const draft = ref<AllSetting>(defaultAllSetting())
const activeTab = ref('panel')

const user = ref({ oldUsername: '', oldPassword: '', newUsername: '', newPassword: '' })

const dirty = computed(() => !settingsEqual(saved.value, draft.value))

async function load(): Promise<void> {
  spinning.value = true
  const msg = await post<Partial<AllSetting>>('xui/setting/all', undefined, true)
  spinning.value = false
  if (msg.success) {
    saved.value = fromServer(msg.obj)
    draft.value = fromServer(msg.obj)
  }
}

async function save(): Promise<void> {
  spinning.value = true
  const msg = await post('xui/setting/update', draft.value)
  spinning.value = false
  if (msg.success) {
    await load()
  }
}

async function updateUser(): Promise<void> {
  spinning.value = true
  const msg = await post('xui/setting/updateUser', user.value)
  spinning.value = false
  if (msg.success) {
    user.value = { oldUsername: '', oldPassword: '', newUsername: '', newPassword: '' }
  }
}

function restartPanel(): void {
  Modal.confirm({
    title: t('restart_panel'),
    content: t('confirm_restart_content'),
    okText: t('confirm'),
    cancelText: t('cancel'),
    onOk: async () => {
      spinning.value = true
      const msg = await post('xui/setting/restartPanel')
      if (msg.success) {
        // 面板正在重新监听端口，等它起来再刷新，否则会撞上一个连接错误页。
        await sleep(5000)
        location.reload()
      }
      spinning.value = false
    },
  })
}

onMounted(load)
</script>

<template>
  <a-spin :spinning="spinning">
    <div class="xui-toolbar xui-glass">
      <h2 class="xui-toolbar__title">{{ t('menu_panel_setting') }}</h2>
      <span class="xui-toolbar__spacer" />
      <a-button type="primary" :disabled="!dirty" @click="save">{{ t('save_config') }}</a-button>
      <a-button danger :disabled="dirty" @click="restartPanel">{{ t('restart_panel') }}</a-button>
    </div>

    <a-tabs v-model:activeKey="activeTab" type="card">
      <a-tab-pane key="panel" :tab="t('tab_panel')">
        <a-list class="xui-panel xui-glass" item-layout="horizontal">
          <SettingListItem
            v-model:value="draft.webListen"
            type="text"
            :title="t('setting_web_listen')"
            :desc="t('setting_web_listen_desc')"
          />
          <SettingListItem
            v-model:value="draft.webPort"
            type="number"
            :title="t('setting_web_port')"
            :desc="t('setting_restart_hint')"
          />
          <SettingListItem
            v-model:value="draft.webCertFile"
            type="text"
            :title="t('setting_cert_file')"
            :desc="t('setting_cert_path_desc')"
          />
          <SettingListItem
            v-model:value="draft.webKeyFile"
            type="text"
            :title="t('setting_key_file')"
            :desc="t('setting_cert_path_desc')"
          />
          <SettingListItem
            v-model:value="draft.webBasePath"
            type="text"
            :title="t('setting_base_path')"
            :desc="t('setting_base_path_desc')"
          />
          <SettingListItem
            v-model:value="draft.webTrustedProxies"
            type="text"
            :title="t('setting_web_trusted_proxies')"
            :desc="t('setting_web_trusted_proxies_desc')"
          />
        </a-list>
      </a-tab-pane>

      <a-tab-pane key="user" :tab="t('tab_user')">
        <div class="xui-stack">
          <a-card :title="t('tab_user')">
            <a-form layout="vertical" style="max-width: 360px">
              <a-form-item :label="t('setting_old_username')">
                <a-input v-model:value="user.oldUsername" />
              </a-form-item>
              <a-form-item :label="t('setting_old_password')">
                <a-input-password v-model:value="user.oldPassword" />
              </a-form-item>
              <a-form-item :label="t('setting_new_username')">
                <a-input v-model:value="user.newUsername" />
              </a-form-item>
              <a-form-item :label="t('setting_new_password')">
                <a-input-password v-model:value="user.newPassword" />
              </a-form-item>
              <a-form-item>
                <a-button type="primary" @click="updateUser">{{ t('edit') }}</a-button>
              </a-form-item>
            </a-form>
          </a-card>
          <TwoFactorCard />
        </div>
      </a-tab-pane>

      <a-tab-pane key="core" :tab="t('tab_core')">
        <a-list class="xui-panel xui-glass" item-layout="horizontal">
          <SettingListItem
            v-model:value="draft.coreTemplateConfig"
            type="textarea"
            :title="t('setting_core_template')"
            :desc="t('setting_core_template_desc')"
          />
        </a-list>
      </a-tab-pane>

      <a-tab-pane key="sub" :tab="t('tab_subscription')">
        <a-list class="xui-panel xui-glass" item-layout="horizontal">
          <SettingListItem
            v-model:value="draft.subAddress"
            type="text"
            :title="t('setting_sub_address')"
            :desc="t('setting_sub_address_desc')"
          />
          <SettingListItem
            v-model:value="draft.metricsToken"
            type="password"
            :title="t('setting_metrics_token')"
            :desc="t('setting_metrics_token_desc')"
          />
        </a-list>
      </a-tab-pane>

      <a-tab-pane key="tg" :tab="t('tab_tg')">
        <a-list class="xui-panel xui-glass" item-layout="horizontal">
          <SettingListItem
            v-model:value="draft.tgBotEnable"
            type="switch"
            :title="t('setting_tg_enable')"
            :desc="t('setting_restart_hint')"
          />
          <SettingListItem
            v-model:value="draft.tgBotToken"
            type="text"
            :title="t('setting_tg_token')"
            :desc="t('setting_restart_hint')"
          />
          <SettingListItem
            v-model:value="draft.tgBotChatId"
            type="number"
            :title="t('setting_tg_chatid')"
            :desc="t('setting_restart_hint')"
          />
          <SettingListItem
            v-model:value="draft.tgRunTime"
            type="text"
            :title="t('setting_tg_runtime')"
            :desc="t('setting_tg_runtime_desc')"
          />
        </a-list>
      </a-tab-pane>

      <a-tab-pane key="other" :tab="t('tab_other')">
        <a-list class="xui-panel xui-glass" item-layout="horizontal">
          <SettingListItem
            v-model:value="draft.timeLocation"
            type="text"
            :title="t('setting_timezone')"
            :desc="t('setting_timezone_desc')"
          />
        </a-list>
      </a-tab-pane>
    </a-tabs>
  </a-spin>
</template>

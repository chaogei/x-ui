<script setup lang="ts">
/**
 * LoginView —— 登录页。
 *
 * 提交仍然是 form-urlencoded POST {basePath}login，带 X-CSRF-Token 头 ——
 * 这份契约被 web/e2e_login_test.go 钉着，不要改成 JSON。
 */
import { LockOutlined, SafetyCertificateOutlined, UserOutlined } from '@ant-design/icons-vue'
import { onMounted, ref } from 'vue'

import { boot, panelUrl, t } from '../boot'
import { post } from '../http'
import { randomIntRange } from '../random'

const username = ref('')
const password = ref('')
const twoFactorCode = ref('')
/** 只有服务端明确说"需要验证码"之后才露出第二因素输入框。 */
const needCode = ref(false)
const loading = ref(false)
const background = ref('')

onMounted(() => {
  const left = randomIntRange(0x222222, 0xffffff / 2).toString(16)
  const right = randomIntRange(0xffffff / 2, 0xdddddd).toString(16)
  background.value = `linear-gradient(${randomIntRange(0, 360)}deg, #${left} 10%, #${right} 100%)`
})

async function login(): Promise<void> {
  loading.value = true
  const msg = await post('login', {
    username: username.value.trim(),
    password: password.value,
    twoFactorCode: twoFactorCode.value.trim(),
  })
  loading.value = false
  if (msg.success) {
    location.href = panelUrl('xui/')
    return
  }
  // 两条提示都意味着"口令对了，缺的是第二因素"，此时把输入框展开。
  if (msg.msg === t('auth_totp_required') || msg.msg === t('auth_totp_invalid')) {
    needCode.value = true
  }
}
</script>

<template>
  <div class="xui-login" :style="{ background }">
    <div class="xui-login__card">
      <h1 class="xui-login__title">{{ t('login') }}</h1>
      <a-card>
        <a-form layout="vertical" @submit.prevent="login">
          <a-form-item>
            <a-input v-model:value="username" :placeholder="t('username')" autofocus @keydown.enter="login">
              <template #prefix><UserOutlined /></template>
            </a-input>
          </a-form-item>
          <a-form-item>
            <a-input-password v-model:value="password" :placeholder="t('password')" @keydown.enter="login">
              <template #prefix><LockOutlined /></template>
            </a-input-password>
          </a-form-item>
          <a-form-item v-if="needCode">
            <a-input
              v-model:value="twoFactorCode"
              :placeholder="t('login_totp_code')"
              autocomplete="one-time-code"
              @keydown.enter="login"
            >
              <template #prefix><SafetyCertificateOutlined /></template>
            </a-input>
          </a-form-item>
          <a-form-item>
            <a-button type="primary" block :loading="loading" @click="login">{{ t('login') }}</a-button>
          </a-form-item>
          <a-form-item v-if="!needCode" style="margin-bottom: 0; text-align: center">
            <a-typography-link @click="needCode = true">{{ t('login_totp_hint') }}</a-typography-link>
          </a-form-item>
        </a-form>
      </a-card>
      <div style="text-align: center; margin-top: 12px">
        <a-typography-text type="secondary" style="color: rgba(255, 255, 255, 0.85)">
          x-ui {{ boot.version }}
        </a-typography-text>
      </div>
    </div>
  </div>
</template>

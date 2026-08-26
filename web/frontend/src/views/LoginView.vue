<script setup lang="ts">
/**
 * LoginView —— 登录页。
 *
 * 提交仍然是 form-urlencoded POST {basePath}login，带 X-CSRF-Token 头 ——
 * 这份契约被 web/e2e_login_test.go 钉着，不要改成 JSON。
 *
 * 背景由 style.css 里那张固定极光图提供，与登录后的页面同一张。旧实现每次
 * 加载随机生成一段线性渐变，观感上每次都是另一个产品，文字对比度还得看运气。
 */
import { LockOutlined, SafetyCertificateOutlined, UserOutlined } from '@ant-design/icons-vue'
import { computed, ref } from 'vue'

import { boot, panelUrl, t } from '../boot'
import { post } from '../http'

const username = ref('')
const password = ref('')
const twoFactorCode = ref('')
/** 只有服务端明确说"需要验证码"之后才露出第二因素输入框。 */
const needCode = ref(false)
const loading = ref(false)
const errorText = ref('')

const canSubmit = computed(() => username.value.trim() !== '' && password.value !== '')

async function login(): Promise<void> {
  if (!canSubmit.value || loading.value) {
    return
  }
  loading.value = true
  errorText.value = ''
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
  errorText.value = msg.msg || t('login_failed')
  // 两条提示都意味着"口令对了，缺的是第二因素"，此时把输入框展开。
  if (msg.msg === t('auth_totp_required') || msg.msg === t('auth_totp_invalid')) {
    needCode.value = true
  }
}
</script>

<template>
  <!-- <main>：登录页没有 AppShell，不写的话整页没有任何地标。 -->
  <main class="xui-login">
    <div class="xui-login__card xui-glass">
      <div class="xui-login__brand">
        <span class="xui-brand__mark" aria-hidden="true">x</span>
        <div>
          <h1 class="xui-login__title">x-ui</h1>
          <p class="xui-login__subtitle">{{ t('login') }}</p>
        </div>
      </div>

      <a-form layout="vertical" @submit.prevent="login">
        <p v-if="errorText" class="xui-login__error" role="alert">{{ errorText }}</p>
        <a-form-item>
          <a-input
            v-model:value="username"
            size="large"
            name="username"
            autocomplete="username"
            :placeholder="t('username')"
            :aria-invalid="!!errorText"
            autofocus
          >
            <template #prefix><UserOutlined /></template>
          </a-input>
        </a-form-item>
        <a-form-item>
          <a-input-password
            v-model:value="password"
            size="large"
            name="password"
            autocomplete="current-password"
            :placeholder="t('password')"
            :aria-invalid="!!errorText"
          >
            <template #prefix><LockOutlined /></template>
          </a-input-password>
        </a-form-item>
        <a-form-item v-if="needCode">
          <a-input
            v-model:value="twoFactorCode"
            size="large"
            inputmode="numeric"
            autocomplete="one-time-code"
            :placeholder="t('login_totp_code')"
            :aria-invalid="!!errorText"
          >
            <template #prefix><SafetyCertificateOutlined /></template>
          </a-input>
        </a-form-item>
        <a-form-item style="margin-bottom: 12px">
          <a-button
            type="primary"
            size="large"
            block
            html-type="submit"
            :loading="loading"
            :disabled="!canSubmit"
          >
            {{ t('login') }}
          </a-button>
        </a-form-item>
        <!--
          <a-typography-link> 渲染出来是一个没有 href 的 <a>，不进 Tab 顺序 ——
          只用键盘的人根本展不开第二因素输入框。这里用按钮。
        -->
        <p v-if="!needCode" class="xui-login__hint">
          <button type="button" class="xui-link-btn" @click="needCode = true">{{ t('login_totp_hint') }}</button>
        </p>
      </a-form>
    </div>

    <p class="xui-login__foot">x-ui {{ boot.version }}</p>
  </main>
</template>

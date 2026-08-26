<script setup lang="ts">
/**
 * TwoFactorCard —— 设置页的两步验证面板。
 *
 * 找回码只在 confirm 那一次返回，之后服务端只存哈希、再也拿不回明文。
 * 因此这里必须在同一屏里把它们展示完，并且提醒用户离开就没了。
 */
import { onMounted, ref } from 'vue'

import { t } from '../boot'
import { post } from '../http'

interface Status {
  enabled: boolean
  pending: boolean
  recoveryCodesLeft: number
}

interface Enrollment {
  secret: string
  otpauthUrl: string
  qrcode: string
}

const status = ref<Status>({ enabled: false, pending: false, recoveryCodesLeft: 0 })
const enrollment = ref<Enrollment | null>(null)
const recoveryCodes = ref<string[]>([])
const code = ref('')
const disablePassword = ref('')
const disableCode = ref('')
const busy = ref(false)

async function refresh(): Promise<void> {
  const msg = await post<Status>('xui/2fa/status', undefined, true)
  if (msg.success && msg.obj) {
    status.value = msg.obj
  }
}

async function beginEnroll(): Promise<void> {
  busy.value = true
  const msg = await post<Enrollment>('xui/2fa/enroll')
  busy.value = false
  if (msg.success && msg.obj) {
    enrollment.value = msg.obj
    recoveryCodes.value = []
    await refresh()
  }
}

async function confirm(): Promise<void> {
  busy.value = true
  const msg = await post<{ recoveryCodes: string[] }>('xui/2fa/confirm', { code: code.value.trim() })
  busy.value = false
  if (!msg.success) {
    return
  }
  recoveryCodes.value = msg.obj?.recoveryCodes ?? []
  enrollment.value = null
  code.value = ''
  await refresh()
}

async function disable(): Promise<void> {
  busy.value = true
  const msg = await post('xui/2fa/disable', {
    password: disablePassword.value,
    code: disableCode.value.trim(),
  })
  busy.value = false
  if (msg.success) {
    disablePassword.value = ''
    disableCode.value = ''
    recoveryCodes.value = []
    await refresh()
  }
}

onMounted(refresh)
</script>

<template>
  <a-card :title="t('twofa_title')">
    <a-space direction="vertical" style="width: 100%" size="middle">
      <div class="xui-tile__row">
        <span class="xui-tile__label">{{ t('twofa_state') }}</span>
        <span :class="status.enabled ? 'xui-chip xui-chip--ok' : 'xui-chip xui-chip--warn'">
          {{ status.enabled ? t('twofa_on') : t('twofa_off') }}
        </span>
        <span v-if="status.pending && !status.enabled" class="xui-chip">{{ t('twofa_pending') }}</span>
        <span v-if="status.enabled" class="xui-chip">
          {{ t('twofa_recovery_left') }}: {{ status.recoveryCodesLeft }}
        </span>
      </div>

      <a-alert type="info" show-icon :message="t('twofa_desc')" />

      <template v-if="!status.enabled">
        <a-button type="primary" :loading="busy" @click="beginEnroll">{{ t('twofa_enroll') }}</a-button>

        <template v-if="enrollment">
          <a-alert type="warning" show-icon :message="t('twofa_scan_hint')" />
          <!-- 同 QrcodeModal：验证器扫的是白底黑码，别把它放在玻璃上。 -->
          <div class="xui-qr__frame" style="width: max-content">
            <img :src="enrollment.qrcode" :alt="t('twofa_title')" style="width: 200px; display: block" />
          </div>
          <div>
            {{ t('twofa_secret') }}:
            <a-typography-text code copyable>{{ enrollment.secret }}</a-typography-text>
          </div>
          <a-space>
            <a-input
              v-model:value="code"
              :placeholder="t('login_totp_code')"
              style="width: 200px"
              autocomplete="one-time-code"
              @keydown.enter="confirm"
            />
            <a-button type="primary" :loading="busy" @click="confirm">{{ t('twofa_confirm') }}</a-button>
          </a-space>
        </template>
      </template>

      <template v-else>
        <a-space direction="vertical" style="width: 100%">
          <a-alert type="warning" show-icon :message="t('twofa_disable_hint')" />
          <a-input-password v-model:value="disablePassword" :placeholder="t('password')" style="max-width: 260px" />
          <a-input
            v-model:value="disableCode"
            :placeholder="t('login_totp_code')"
            style="max-width: 260px"
            autocomplete="one-time-code"
          />
          <a-button danger :loading="busy" @click="disable">{{ t('twofa_disable') }}</a-button>
        </a-space>
      </template>

      <template v-if="recoveryCodes.length">
        <a-alert type="error" show-icon :message="t('twofa_recovery_once')" />
        <div class="xui-recovery-codes">
          <span v-for="rc in recoveryCodes" :key="rc">{{ rc }}</span>
        </div>
      </template>
    </a-space>
  </a-card>
</template>

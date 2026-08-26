<script setup lang="ts">
/**
 * TlsFields —— 各协议共用的 TLS 块（settings.tls 指向一个 TlsBlock）。
 */
import { t } from '../boot'
import type { TlsBlock } from '../models/core'

const props = defineProps<{ tls: TlsBlock }>()

function setAlpn(raw: string): void {
  props.tls.alpn = (raw || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}
</script>

<template>
  <a-divider>TLS</a-divider>
  <div class="xui-form-grid">
    <a-form-item :label="t('tls_enable')">
      <a-switch v-model:checked="tls.enabled" />
    </a-form-item>
    <template v-if="tls.enabled">
      <a-form-item :label="t('tls_sni')">
        <a-input v-model:value="tls.server_name" />
      </a-form-item>
      <a-form-item :label="t('tls_alpn')">
        <a-input :value="tls.alpn.join(',')" @change="(e: any) => setAlpn(e.target.value)" />
      </a-form-item>
      <a-form-item :label="t('tls_cert_source')">
        <a-radio-group v-model:value="tls.useFile" button-style="solid">
          <a-radio-button :value="true">{{ t('tls_source_file') }}</a-radio-button>
          <a-radio-button :value="false">{{ t('tls_source_content') }}</a-radio-button>
        </a-radio-group>
      </a-form-item>
      <template v-if="tls.useFile">
        <a-form-item :label="t('tls_cert_path')">
          <a-input v-model:value="tls.certificate_path" />
        </a-form-item>
        <a-form-item :label="t('tls_key_path')">
          <a-input v-model:value="tls.key_path" />
        </a-form-item>
      </template>
      <template v-else>
        <a-form-item :label="t('tls_cert_content')">
          <a-textarea v-model:value="tls.certificate" :rows="2" />
        </a-form-item>
        <a-form-item :label="t('tls_key_content')">
          <a-textarea v-model:value="tls.key" :rows="2" />
        </a-form-item>
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
/**
 * TransportFields —— 各协议共用的 transport 块。
 */
import { t } from '../boot'
import type { TransportBlock } from '../models/core'

const props = defineProps<{ transport: TransportBlock }>()

function setHost(raw: string): void {
  props.transport.host = (raw || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}
</script>

<template>
  <a-divider>transport</a-divider>
  <div class="xui-form-grid">
    <a-form-item label="type">
      <a-select v-model:value="transport.type" style="width: 160px">
        <a-select-option value="">{{ t('transport_none') }}</a-select-option>
        <a-select-option value="ws">websocket</a-select-option>
        <a-select-option value="httpupgrade">httpupgrade</a-select-option>
        <a-select-option value="http">http</a-select-option>
        <a-select-option value="grpc">grpc</a-select-option>
        <a-select-option value="quic">quic</a-select-option>
      </a-select>
    </a-form-item>
    <a-form-item v-if="transport.type === 'ws' || transport.type === 'httpupgrade'" label="path">
      <a-input v-model:value="transport.path" placeholder="/" />
    </a-form-item>
    <template v-if="transport.type === 'http'">
      <a-form-item label="path">
        <a-input v-model:value="transport.path" />
      </a-form-item>
      <a-form-item :label="t('transport_host')">
        <a-input :value="transport.host.join(',')" @change="(e: any) => setHost(e.target.value)" />
      </a-form-item>
    </template>
    <a-form-item v-if="transport.type === 'grpc'" label="service_name">
      <a-input v-model:value="transport.service_name" />
    </a-form-item>
  </div>
</template>

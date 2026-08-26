<script setup lang="ts">
/**
 * SniffFields —— sing-box 的 sniff 是 inbound 顶层字段，不再嵌在 sniffing 对象里。
 */
import { QuestionCircleFilled } from '@ant-design/icons-vue'

import { t } from '../boot'
import type { SniffBlock } from '../models/core'

defineProps<{ sniff: SniffBlock }>()
</script>

<template>
  <a-divider>sniff</a-divider>
  <div class="xui-form-grid">
    <a-form-item>
      <template #label>
        sniff
        <a-tooltip :title="t('sniff_desc')"><QuestionCircleFilled /></a-tooltip>
      </template>
      <a-switch v-model:checked="sniff.sniff" />
    </a-form-item>
    <a-form-item :label="t('sniff_override')">
      <a-switch v-model:checked="sniff.sniff_override_destination" />
    </a-form-item>
    <a-form-item :label="t('sniff_timeout_label')">
      <a-input v-model:value="sniff.sniff_timeout" :placeholder="t('sniff_timeout_ph')" />
    </a-form-item>
    <a-form-item label="domain_strategy">
      <a-select v-model:value="sniff.domain_strategy" style="width: 180px">
        <a-select-option value="">{{ t('sniff_default') }}</a-select-option>
        <a-select-option value="prefer_ipv4">prefer_ipv4</a-select-option>
        <a-select-option value="prefer_ipv6">prefer_ipv6</a-select-option>
        <a-select-option value="ipv4_only">ipv4_only</a-select-option>
        <a-select-option value="ipv6_only">ipv6_only</a-select-option>
      </a-select>
    </a-form-item>
  </div>
</template>

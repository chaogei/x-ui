<script setup lang="ts">
/**
 * InboundModal —— 新建/编辑入站。
 */
import { QuestionCircleFilled } from '@ant-design/icons-vue'
import dayjs, { type Dayjs } from 'dayjs'
import { computed } from 'vue'

import ProtocolFields from './ProtocolFields.vue'
import RealityFields from './RealityFields.vue'
import SniffFields from './SniffFields.vue'
import TlsFields from './TlsFields.vue'
import TransportFields from './TransportFields.vue'
import { t } from '../boot'
import { allProtocolKeys, type Inbound } from '../models/core'
import { protocolForms } from '../models/forms'
import type { DBInbound } from '../models/inbound'

const props = defineProps<{
  open: boolean
  title: string
  okText: string
  confirmLoading: boolean
  inbound: Inbound
  dbInbound: DBInbound
}>()

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  (e: 'ok'): void
}>()

const protocols = allProtocolKeys()

const form = computed(() => protocolForms[props.inbound.protocol] ?? { fields: [] })

const expiry = computed<Dayjs | null>({
  get: () => (props.dbInbound.expiryTime ? dayjs(props.dbInbound.expiryTime) : null),
  set: (v) => {
    props.dbInbound.expiryTime = v ? v.valueOf() : 0
  },
})

const totalGB = computed<number>({
  get: () => props.dbInbound.totalGB,
  set: (v) => {
    props.dbInbound.totalGB = v
  },
})
</script>

<template>
  <a-modal
    :open="open"
    :title="title"
    :ok-text="okText"
    :cancel-text="t('close')"
    :confirm-loading="confirmLoading"
    :mask-closable="false"
    width="900px"
    @update:open="(v: boolean) => emit('update:open', v)"
    @ok="emit('ok')"
  >
    <div class="xui-form-grid">
      <a-form-item :label="t('remark')">
        <a-input v-model:value="dbInbound.remark" />
      </a-form-item>
      <a-form-item :label="t('enable')">
        <a-switch v-model:checked="dbInbound.enable" />
      </a-form-item>
      <a-form-item :label="t('protocol')">
        <a-select v-model:value="inbound.protocol" style="width: 180px">
          <a-select-option v-for="p in protocols" :key="p" :value="p">{{ p }}</a-select-option>
        </a-select>
      </a-form-item>
      <a-form-item>
        <template #label>
          {{ t('form_listen') }}
          <a-tooltip :title="t('form_listen_desc')"><QuestionCircleFilled /></a-tooltip>
        </template>
        <a-input v-model:value="inbound.listen" />
      </a-form-item>
      <a-form-item :label="t('port')">
        <a-input-number v-model:value="inbound.port" :min="1" :max="65535" />
      </a-form-item>
      <a-form-item>
        <template #label>
          {{ t('form_total_gb') }}
          <a-tooltip :title="t('form_total_gb_desc')"><QuestionCircleFilled /></a-tooltip>
        </template>
        <a-input-number v-model:value="totalGB" :min="0" />
      </a-form-item>
      <a-form-item>
        <template #label>
          {{ t('expiry_time') }}
          <a-tooltip :title="t('form_expiry_time_desc')"><QuestionCircleFilled /></a-tooltip>
        </template>
        <a-date-picker v-model:value="expiry" show-time format="YYYY-MM-DD HH:mm" style="width: 260px" />
      </a-form-item>
    </div>

    <a-divider>{{ inbound.protocol }}</a-divider>
    <ProtocolFields :settings="inbound.settings" :fields="form.fields" />

    <template v-if="form.extra">
      <a-divider>{{ t(form.extraTitle ?? '') }}</a-divider>
      <ProtocolFields :settings="inbound.settings" :fields="form.extra" />
    </template>

    <TlsFields v-if="form.tls && inbound.settings.tls" :tls="inbound.settings.tls" />
    <RealityFields v-if="form.reality && inbound.settings.tls?.enabled" :tls="inbound.settings.tls" />
    <TransportFields v-if="form.transport && inbound.settings.transport" :transport="inbound.settings.transport" />
    <SniffFields v-if="inbound.canSniff" :sniff="inbound.sniff" />
  </a-modal>
</template>

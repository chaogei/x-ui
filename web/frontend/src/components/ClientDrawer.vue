<script setup lang="ts">
/**
 * ClientDrawer —— 某个入站下的多用户管理。
 *
 * 展示哪些凭证输入框由后端 UserSchema 决定（identifier + credentials），
 * 前端不再为每种协议写死一套字段：新增协议时后端改注册表即可。
 */
import { Modal } from 'ant-design-vue'
import dayjs, { type Dayjs } from 'dayjs'
import { computed, ref, watch } from 'vue'

import QrcodeModal from './QrcodeModal.vue'
import { boot, t } from '../boot'
import { formatMillis, sizeFormat } from '../format'
import { post } from '../http'
import { ProtocolSpecs } from '../models/core'
import { emptyClient, ONE_GB, type Client } from '../models/inbound'
import { randomSeq, randomUUID } from '../random'

const props = defineProps<{
  open: boolean
  inboundId: number
  protocol: string
  remark: string
}>()

const emit = defineEmits<{
  (e: 'update:open', v: boolean): void
  (e: 'changed'): void
}>()

const clients = ref<Client[]>([])
const loading = ref(false)

const editorOpen = ref(false)
const editing = ref<Client>(emptyClient(0))
const editingIsNew = ref(true)

const qrOpen = ref(false)
const qrText = ref('')

/** 该协议的用户凭证字段名，例如 vmess -> ['uuid']、tuic -> ['uuid','password']。 */
const credentialFields = computed<string[]>(() => {
  const users = ProtocolSpecs[props.protocol]?.users
  if (!users || !users.identifier) {
    return []
  }
  return [users.identifier, ...(users.credentials ?? [])].filter(Boolean)
})

/** 协议没有用户维度（direct / wireguard）时整个抽屉都没有意义。 */
const supportsClients = computed(() => credentialFields.value.length > 0)

const expiry = computed<Dayjs | null>({
  get: () => (editing.value.expiryTime ? dayjs(editing.value.expiryTime) : null),
  set: (v) => {
    editing.value.expiryTime = v ? v.valueOf() : 0
  },
})

const totalGB = computed<number>({
  get: () => Math.round((editing.value.total / ONE_GB) * 100) / 100,
  set: (v) => {
    editing.value.total = Math.round((Number(v) || 0) * ONE_GB)
  },
})

function subUrl(client: Client): string {
  return `${location.origin}${boot.basePath}sub/${client.subToken}`
}

async function load(): Promise<void> {
  if (!props.inboundId) {
    return
  }
  loading.value = true
  const msg = await post<Client[]>(`xui/client/list/${props.inboundId}`, undefined, true)
  loading.value = false
  clients.value = msg.success ? (msg.obj ?? []) : []
}

watch(
  () => [props.open, props.inboundId],
  () => {
    if (props.open) {
      void load()
    }
  },
  { immediate: true },
)

function openAdd(): void {
  const client = emptyClient(props.inboundId)
  // 预填随机凭证，用户想自己指定再覆盖即可。
  for (const field of credentialFields.value) {
    if (field === 'uuid') client.uuid = randomUUID()
    if (field === 'password') client.password = randomSeq(16)
    if (field === 'username') client.username = randomSeq(8)
  }
  editing.value = client
  editingIsNew.value = true
  editorOpen.value = true
}

function openEdit(client: Client): void {
  editing.value = { ...client }
  editingIsNew.value = false
  editorOpen.value = true
}

async function save(): Promise<void> {
  const client = editing.value
  const url = editingIsNew.value ? 'xui/client/add' : `xui/client/update/${client.id}`
  const msg = await post(url, client)
  if (!msg.success) {
    return
  }
  editorOpen.value = false
  await load()
  emit('changed')
}

function remove(client: Client): void {
  Modal.confirm({
    title: t('client_delete'),
    content: t('client_delete_confirm'),
    okText: t('delete'),
    cancelText: t('cancel'),
    okType: 'danger',
    onOk: async () => {
      if ((await post(`xui/client/del/${client.id}`)).success) {
        await load()
        emit('changed')
      }
    },
  })
}

function resetTraffic(client: Client): void {
  Modal.confirm({
    title: t('reset_traffic'),
    content: t('confirm_reset_traffic_content'),
    okText: t('reset'),
    cancelText: t('cancel'),
    onOk: async () => {
      if ((await post(`xui/client/resetTraffic/${client.id}`)).success) {
        await load()
      }
    },
  })
}

function rotate(client: Client): void {
  Modal.confirm({
    title: t('client_rotate_token'),
    content: t('client_rotate_token_confirm'),
    okText: t('confirm'),
    cancelText: t('cancel'),
    onOk: async () => {
      if ((await post(`xui/client/rotateToken/${client.id}`)).success) {
        await load()
      }
    },
  })
}

function showSub(client: Client): void {
  qrText.value = subUrl(client)
  qrOpen.value = true
}

const columns = computed(() => [
  { title: t('client_email'), dataIndex: 'email', key: 'email' },
  { title: t('enable'), key: 'enable', width: 90 },
  { title: t('traffic_up_down'), key: 'traffic', width: 200 },
  { title: t('expiry_time'), key: 'expiry', width: 180 },
  { title: t('client_subscription'), key: 'sub', width: 120 },
  { title: t('operation'), key: 'action', width: 220 },
])
</script>

<template>
  <a-drawer
    :open="open"
    :title="`${t('client_list')} — ${remark || '#' + inboundId}`"
    width="960"
    @update:open="(v: boolean) => emit('update:open', v)"
  >
    <a-alert v-if="!supportsClients" type="info" show-icon :message="t('client_unsupported_protocol')" />
    <template v-else>
      <a-space style="margin-bottom: 12px">
        <a-button type="primary" @click="openAdd">{{ t('client_add') }}</a-button>
        <a-button @click="load">{{ t('refresh') }}</a-button>
      </a-space>
      <a-alert type="info" show-icon :message="t('client_traffic_note')" style="margin-bottom: 12px" />

      <a-table :columns="columns" :data-source="clients" :loading="loading" row-key="id" :pagination="false" size="small">
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'enable'">
            <a-tag :color="record.enable ? 'green' : 'red'">{{ record.enable ? t('enable') : t('disable') }}</a-tag>
          </template>
          <template v-else-if="column.key === 'traffic'">
            <a-tag color="blue">{{ sizeFormat(record.up) }} / {{ sizeFormat(record.down) }}</a-tag>
            <a-tag v-if="record.total > 0" :color="record.up + record.down < record.total ? 'cyan' : 'red'">
              {{ sizeFormat(record.total) }}
            </a-tag>
            <a-tag v-else color="green">{{ t('no_limit') }}</a-tag>
          </template>
          <template v-else-if="column.key === 'expiry'">
            <a-tag v-if="record.expiryTime > 0" :color="record.expiryTime < Date.now() ? 'red' : 'blue'">
              {{ formatMillis(record.expiryTime) }}
            </a-tag>
            <a-tag v-else color="green">{{ t('unlimited') }}</a-tag>
          </template>
          <template v-else-if="column.key === 'sub'">
            <a-button type="link" size="small" @click="showSub(record)">{{ t('client_show_sub') }}</a-button>
          </template>
          <template v-else-if="column.key === 'action'">
            <a-space>
              <a @click="openEdit(record)">{{ t('edit') }}</a>
              <a @click="resetTraffic(record)">{{ t('reset_traffic') }}</a>
              <a @click="rotate(record)">{{ t('client_rotate_token') }}</a>
              <a style="color: #ff4d4f" @click="remove(record)">{{ t('delete') }}</a>
            </a-space>
          </template>
        </template>
      </a-table>
    </template>

    <a-modal
      v-model:open="editorOpen"
      :title="editingIsNew ? t('client_add') : t('client_edit')"
      :ok-text="t('confirm')"
      :cancel-text="t('cancel')"
      @ok="save"
    >
      <div class="xui-form-grid">
        <a-form-item :label="t('client_email')">
          <a-input v-model:value="editing.email" :placeholder="t('client_email_ph')" />
        </a-form-item>
        <a-form-item :label="t('enable')">
          <a-switch v-model:checked="editing.enable" />
        </a-form-item>
        <a-form-item v-if="credentialFields.includes('uuid')" label="UUID">
          <a-input v-model:value="editing.uuid" />
        </a-form-item>
        <a-form-item v-if="credentialFields.includes('password')" :label="t('proto_password')">
          <a-input v-model:value="editing.password" />
        </a-form-item>
        <a-form-item v-if="credentialFields.includes('username')" :label="t('proto_username')">
          <a-input v-model:value="editing.username" />
        </a-form-item>
        <a-form-item :label="t('form_total_gb')">
          <a-input-number v-model:value="totalGB" :min="0" />
        </a-form-item>
        <a-form-item :label="t('expiry_time')">
          <a-date-picker v-model:value="expiry" show-time format="YYYY-MM-DD HH:mm" style="width: 240px" />
        </a-form-item>
        <a-form-item :label="t('client_extra')">
          <a-textarea v-model:value="editing.extra" :rows="2" :placeholder="t('client_extra_ph')" />
        </a-form-item>
      </div>
    </a-modal>

    <QrcodeModal v-model:open="qrOpen" :title="t('client_subscription')" :text="qrText" />
  </a-drawer>
</template>

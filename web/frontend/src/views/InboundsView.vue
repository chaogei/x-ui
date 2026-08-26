<script setup lang="ts">
/**
 * InboundsView —— 入站列表 + CRUD + 每个入站下的多用户。
 */
import { InboxOutlined, PlusOutlined } from '@ant-design/icons-vue'
import { Modal } from 'ant-design-vue'
import { computed, onMounted, ref } from 'vue'

import ClientDrawer from '../components/ClientDrawer.vue'
import InboundInfoModal from '../components/InboundInfoModal.vue'
import InboundModal from '../components/InboundModal.vue'
import QrcodeModal from '../components/QrcodeModal.vue'
import { boot, t } from '../boot'
import { formatMillis, sizeFormat } from '../format'
import { post } from '../http'
import { Inbound } from '../models/core'
import { DBInbound, type DBInboundData } from '../models/inbound'

const spinning = ref(false)
const dbInbounds = ref<DBInbound[]>([])
const inbounds = ref<Inbound[]>([])

const modalOpen = ref(false)
const modalTitle = ref('')
const modalOkText = ref('')
const modalLoading = ref(false)
const editingId = ref(0)
const editInbound = ref<Inbound>(new Inbound())
const editDbInbound = ref<DBInbound>(new DBInbound())

const infoOpen = ref(false)
const infoDb = ref<DBInbound | null>(null)
const infoInbound = ref<Inbound | null>(null)

const qrOpen = ref(false)
const qrText = ref('')

const clientsOpen = ref(false)
const clientsInbound = ref<DBInbound>(new DBInbound())

const total = computed(() =>
  dbInbounds.value.reduce((acc, i) => ({ up: acc.up + i.up, down: acc.down + i.down }), { up: 0, down: 0 }),
)

async function load(): Promise<void> {
  spinning.value = true
  const msg = await post<DBInboundData[]>('xui/inbound/list', undefined, true)
  spinning.value = false
  if (!msg.success) {
    return
  }
  const rows = (msg.obj ?? []).map((row) => new DBInbound(row))
  dbInbounds.value = rows
  inbounds.value = rows.map((row) => row.toInbound())
}

function openAdd(): void {
  editingId.value = 0
  editInbound.value = new Inbound()
  editDbInbound.value = new DBInbound()
  modalTitle.value = t('add_inbound')
  modalOkText.value = t('add')
  modalOpen.value = true
}

function openEdit(row: DBInbound): void {
  editingId.value = row.id
  editInbound.value = row.toInbound()
  editDbInbound.value = new DBInbound(row)
  modalTitle.value = t('edit_inbound')
  modalOkText.value = t('edit')
  modalOpen.value = true
}

/** buildPayload 把 viewmodel 拍平成后端 model.Inbound 的表单字段。 */
function buildPayload(inbound: Inbound, db: DBInbound): Record<string, unknown> {
  const snapshot = inbound.toJson()
  return {
    up: db.up,
    down: db.down,
    total: db.total,
    remark: db.remark,
    enable: db.enable,
    expiryTime: db.expiryTime,
    listen: snapshot.listen,
    port: snapshot.port,
    protocol: snapshot.protocol,
    settings: JSON.stringify(snapshot.settings ?? {}),
    sniffing: inbound.canSniff ? JSON.stringify(snapshot.sniffing ?? {}) : '{}',
  }
}

async function submitModal(): Promise<void> {
  modalLoading.value = true
  const url = editingId.value ? `xui/inbound/update/${editingId.value}` : 'xui/inbound/add'
  const msg = await post(url, buildPayload(editInbound.value, editDbInbound.value))
  modalLoading.value = false
  if (msg.success) {
    modalOpen.value = false
    await load()
  }
}

async function saveRow(row: DBInbound): Promise<void> {
  const msg = await post(`xui/inbound/update/${row.id}`, buildPayload(row.toInbound(), row))
  if (msg.success) {
    await load()
  }
}

function resetTraffic(row: DBInbound): void {
  Modal.confirm({
    title: t('reset_traffic'),
    content: t('confirm_reset_traffic_content'),
    okText: t('reset'),
    cancelText: t('cancel'),
    onOk: async () => {
      const copy = new DBInbound(row)
      copy.up = 0
      copy.down = 0
      await saveRow(copy)
    },
  })
}

function remove(row: DBInbound): void {
  Modal.confirm({
    title: t('delete_inbound'),
    content: t('confirm_delete_inbound_content'),
    okText: t('delete'),
    cancelText: t('cancel'),
    okType: 'danger',
    onOk: async () => {
      if ((await post(`xui/inbound/del/${row.id}`)).success) {
        await load()
      }
    },
  })
}

function showInfo(row: DBInbound): void {
  infoDb.value = row
  infoInbound.value = row.toInbound()
  infoOpen.value = true
}

function showQrcode(row: DBInbound): void {
  qrText.value = row.genLink()
  qrOpen.value = true
}

function showClients(row: DBInbound): void {
  clientsInbound.value = row
  clientsOpen.value = true
}

const columns = computed(() => [
  { title: t('enable'), key: 'enable', width: 80, align: 'center' as const },
  { title: t('inbound_id'), dataIndex: 'id', key: 'id', width: 60, align: 'center' as const },
  { title: t('remark'), dataIndex: 'remark', key: 'remark', width: 140 },
  { title: t('protocol'), key: 'protocol', width: 110, align: 'center' as const },
  { title: t('port'), dataIndex: 'port', key: 'port', width: 80, align: 'center' as const },
  { title: t('traffic_up_down'), key: 'traffic', width: 210, align: 'center' as const },
  { title: t('stream_settings'), key: 'stream', width: 140, align: 'center' as const },
  { title: t('expiry_time'), key: 'expiryTime', width: 170, align: 'center' as const },
  // 操作列钉在右侧：所有列加起来比常见视口宽，表格会横向滚动，而这一列正是
  // 用得最多的。不钉住的话它默认落在滚动区最右端，开箱就是看不见的状态。
  { title: t('operation'), key: 'action', width: 280, align: 'center' as const, fixed: 'right' as const },
])

onMounted(load)
</script>

<template>
  <a-spin :spinning="spinning">
    <a-alert
      v-if="boot.initialCredentials"
      type="error"
      show-icon
      style="margin-bottom: 18px"
      :message="t('warn_initial_credentials')"
    />

    <div class="xui-tiles" style="margin-bottom: 18px">
      <article class="xui-tile xui-glass">
        <span class="xui-tile__label">{{ t('total_up_down') }}</span>
        <p class="xui-tile__value">{{ sizeFormat(total.up) }} / {{ sizeFormat(total.down) }}</p>
      </article>
      <article class="xui-tile xui-glass">
        <span class="xui-tile__label">{{ t('total_used') }}</span>
        <p class="xui-tile__value">{{ sizeFormat(total.up + total.down) }}</p>
      </article>
      <article class="xui-tile xui-glass">
        <span class="xui-tile__label">{{ t('inbound_count') }}</span>
        <p class="xui-tile__value">{{ dbInbounds.length }}</p>
      </article>
    </div>

    <div class="xui-toolbar xui-glass">
      <h2 class="xui-toolbar__title">{{ t('menu_inbound_list') }}</h2>
      <span class="xui-toolbar__spacer" />
      <a-button type="primary" @click="openAdd">
        <template #icon><PlusOutlined /></template>
        {{ t('add_inbound') }}
      </a-button>
    </div>

    <div class="xui-panel xui-panel--flush xui-glass xui-table">
      <a-table
        :columns="columns"
        :data-source="dbInbounds"
        :loading="spinning"
        row-key="id"
        :pagination="false"
        :scroll="{ x: 1300 }"
        size="middle"
      >
        <template #emptyText>
          <div class="xui-empty">
            <span class="xui-empty__mark" aria-hidden="true"><InboxOutlined /></span>
            <p class="xui-empty__title">{{ t('inbound_empty') }}</p>
            <p class="xui-empty__hint">{{ t('inbound_empty_hint') }}</p>
          </div>
        </template>
        <template #bodyCell="{ column, record, index }">
          <template v-if="column.key === 'enable'">
            <a-switch :checked="record.enable" @change="(v: any) => { record.enable = !!v; saveRow(record) }" />
          </template>
          <template v-else-if="column.key === 'protocol'">
            <a-tag color="blue">{{ record.protocol }}</a-tag>
          </template>
          <template v-else-if="column.key === 'traffic'">
            <a-tag color="blue">{{ sizeFormat(record.up) }} / {{ sizeFormat(record.down) }}</a-tag>
            <a-tag v-if="record.total > 0" :color="record.up + record.down < record.total ? 'cyan' : 'red'">
              {{ sizeFormat(record.total) }}
            </a-tag>
            <a-tag v-else color="green">{{ t('no_limit') }}</a-tag>
          </template>
          <template v-else-if="column.key === 'stream'">
            <a-tag v-if="inbounds[index]?.transportType" color="green">{{ inbounds[index].transportType }}</a-tag>
            <a-tag v-if="inbounds[index]?.tls" color="blue">tls</a-tag>
            <a-tag v-if="inbounds[index]?.settings?.tls?.reality?.enabled" color="purple">reality</a-tag>
            <span v-if="!inbounds[index]?.transportType && !inbounds[index]?.tls">{{ t('none') }}</span>
          </template>
          <template v-else-if="column.key === 'expiryTime'">
            <a-tag v-if="record.expiryTime > 0" :color="record.isExpiry ? 'red' : 'blue'">
              {{ formatMillis(record.expiryTime) }}
            </a-tag>
            <a-tag v-else color="green">{{ t('unlimited') }}</a-tag>
          </template>
          <template v-else-if="column.key === 'action'">
            <a-space :size="4" wrap>
              <a @click="showInfo(record)">{{ t('view') }}</a>
              <a @click="showClients(record)">{{ t('client_list') }}</a>
              <a v-if="record.hasLink()" @click="showQrcode(record)">{{ t('qrcode') }}</a>
              <a @click="openEdit(record)">{{ t('edit') }}</a>
              <a @click="resetTraffic(record)">{{ t('reset_traffic') }}</a>
              <a class="xui-danger-link" @click="remove(record)">{{ t('delete') }}</a>
            </a-space>
          </template>
        </template>
      </a-table>
    </div>

    <InboundModal
      v-model:open="modalOpen"
      :title="modalTitle"
      :ok-text="modalOkText"
      :confirm-loading="modalLoading"
      :inbound="editInbound"
      :db-inbound="editDbInbound"
      @ok="submitModal"
    />
    <InboundInfoModal v-model:open="infoOpen" :db-inbound="infoDb" :inbound="infoInbound" />
    <QrcodeModal v-model:open="qrOpen" :title="t('qrcode')" :text="qrText" />
    <ClientDrawer
      v-model:open="clientsOpen"
      :inbound-id="clientsInbound.id"
      :protocol="clientsInbound.protocol"
      :remark="clientsInbound.remark"
      @changed="load"
    />
  </a-spin>
</template>

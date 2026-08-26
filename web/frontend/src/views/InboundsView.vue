<script setup lang="ts">
/**
 * InboundsView —— 入站列表 + CRUD + 每个入站下的多用户。
 */
import { InboxOutlined, PlusOutlined } from '@ant-design/icons-vue'
import { Modal } from 'ant-design-vue'
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

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
const loadError = ref('')
const filterText = ref('')

/**
 * 每行的传输层摘要。
 *
 * 表格与卡片列表只需要"transport / tls / reality"三个标签，但算出它们要把
 * settings 那段 JSON 解析成一个完整的 Inbound。所以在 load() 里一次性算完
 * 缓存起来，而不是：
 *
 *   - 放进 computed —— 拨一下某行的启用开关就会让整张表重新解析一遍 JSON；
 *   - 在模板里现算 —— 每次重绘都解析一遍，还会造出一批新对象让 diff 全部落空。
 *
 * 键用 id 而不是下标：表格与卡片列表各自遍历自己的数组，靠位置对齐的第二个
 * 数组只要有一处顺序不一致（将来加排序、过滤），标签就会挂到别人身上。
 */
interface StreamSummary {
  transport: string
  tls: boolean
  reality: boolean
}

const NO_STREAM: StreamSummary = { transport: '', tls: false, reality: false }

const streams = ref(new Map<number, StreamSummary>())

function streamOf(row: DBInbound): StreamSummary {
  return streams.value.get(row.id) ?? NO_STREAM
}

/** 卡片列表里同样三段信息，摊成一行文字。 */
function streamLabel(row: DBInbound): string {
  const s = streamOf(row)
  return [s.transport, s.tls ? 'tls' : '', s.reality ? 'reality' : ''].filter(Boolean).join(' · ') || t('none')
}

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

const visibleInbounds = computed(() => {
  const q = filterText.value.trim().toLowerCase()
  if (!q) {
    return dbInbounds.value
  }
  return dbInbounds.value.filter((row) => {
    const remark = (row.remark || '').toLowerCase()
    const protocol = (row.protocol || '').toLowerCase()
    return remark.includes(q) || protocol.includes(q) || String(row.port).includes(q)
  })
})

const filterEmpty = computed(() => dbInbounds.value.length > 0 && visibleInbounds.value.length === 0)

async function load(): Promise<void> {
  spinning.value = true
  loadError.value = ''
  const msg = await post<DBInboundData[]>('xui/inbound/list', undefined, true)
  spinning.value = false
  if (!msg.success) {
    loadError.value = msg.msg || t('load_failed')
    return
  }
  const rows = (msg.obj ?? []).map((row) => new DBInbound(row))
  const summaries = new Map<number, StreamSummary>()
  for (const row of rows) {
    const inbound = row.toInbound()
    summaries.set(row.id, {
      transport: inbound.transportType,
      tls: inbound.tls,
      reality: !!inbound.settings?.tls?.reality?.enabled,
    })
  }
  dbInbounds.value = rows
  streams.value = summaries
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

/**
 * buildPayload 把 viewmodel 拍平成后端 model.Inbound 的表单字段。
 *
 * 不带 up/down：页面上的那两个数字是 load() 那一刻的快照，而流量任务每 10 秒
 * 就把内核计数器累加进这两列。回传快照等于把这中间跑过的字节抹掉——拨一下
 * 启用开关就够了。清零走 xui/inbound/resetTraffic。
 */
function buildPayload(inbound: Inbound, db: DBInbound): Record<string, unknown> {
  const snapshot = inbound.toJson()
  return {
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
      if ((await post(`xui/inbound/resetTraffic/${row.id}`)).success) {
        await load()
      }
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

// 列宽合计 1150，正好塞得进 1440 视口减去侧边栏与页边距之后剩下的宽度：
// 常见桌面尺寸下表格根本不横向滚动，也就不会有任何一列被钉住的操作列压住。
const columns = computed(() => [
  { title: t('enable'), key: 'enable', width: 68, align: 'center' as const },
  { title: t('inbound_id'), dataIndex: 'id', key: 'id', width: 52, align: 'center' as const },
  { title: t('remark'), dataIndex: 'remark', key: 'remark', width: 150 },
  { title: t('protocol'), key: 'protocol', width: 96, align: 'center' as const },
  { title: t('port'), dataIndex: 'port', key: 'port', width: 76, align: 'center' as const },
  { title: t('traffic_up_down'), key: 'traffic', width: 190, align: 'center' as const },
  { title: t('stream_settings'), key: 'stream', width: 120, align: 'center' as const },
  { title: t('expiry_time'), key: 'expiryTime', width: 150, align: 'center' as const },
  // 更窄的视口下表格仍会横向滚动，此时操作列必须钉在右侧：不钉住的话它落在
  // 滚动区最右端，开箱就是看不见的状态。
  { title: t('operation'), key: 'action', width: 248, align: 'center' as const, fixed: 'right' as const },
])

/**
 * 窄屏改用卡片列表。
 *
 * 一张 1150px 的表格塞进 360px 的视口，横向滚动之外还有一列钉在右边——
 * 操作列一个人就占掉视口的三分之二，剩下的部分谁也读不了。手机上把每个入站
 * 摊成一张卡片，信息一条不少，而且不用横向滚。
 */
const narrow = ref(false)

let mediaQuery: MediaQueryList | undefined

function onNarrowChange(e: MediaQueryListEvent | MediaQueryList): void {
  narrow.value = e.matches
}

onMounted(() => {
  void load()

  // 断点与 style.css 里那几条窄屏规则同一个值，改一处要改两处。
  mediaQuery = window.matchMedia?.('(max-width: 768px)')
  if (!mediaQuery) {
    return
  }
  narrow.value = mediaQuery.matches
  mediaQuery.addEventListener('change', onNarrowChange)
})

// 监听器挂在 MediaQueryList 上，它的生命周期跟着 window 走而不是跟着组件走：
// 不摘掉的话，卸载后的组件仍然会被回调持有，也仍然会去写它的 ref。
onBeforeUnmount(() => {
  mediaQuery?.removeEventListener('change', onNarrowChange)
  mediaQuery = undefined
})
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
    <a-alert
      v-if="loadError"
      type="error"
      show-icon
      style="margin-bottom: 18px"
      :message="loadError"
    >
      <template #action>
        <a-button size="small" @click="load">{{ t('action_retry') }}</a-button>
      </template>
    </a-alert>

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
      <a-input
        v-model:value="filterText"
        class="xui-toolbar__filter"
        allow-clear
        :placeholder="t('inbound_filter_ph')"
        :aria-label="t('inbound_filter_ph')"
      />
      <span class="xui-toolbar__spacer" />
      <a-button type="primary" @click="openAdd">
        <template #icon><PlusOutlined /></template>
        {{ t('add_inbound') }}
      </a-button>
    </div>

    <div v-if="narrow" class="xui-cards">
      <article v-for="row in visibleInbounds" :key="row.id" class="xui-card-row xui-glass">
        <header class="xui-card-row__head">
          <a-switch
            :checked="row.enable"
            :aria-label="`${t('enable')} — ${row.remark || '#' + row.id}`"
            @change="(v: any) => { row.enable = !!v; saveRow(row) }"
          />
          <span class="xui-card-row__name">{{ row.remark || '#' + row.id }}</span>
          <a-tag color="blue">{{ row.protocol }}</a-tag>
        </header>
        <dl class="xui-card-row__facts">
          <div>
            <dt>{{ t('port') }}</dt>
            <dd>{{ row.port }}</dd>
          </div>
          <div>
            <dt>{{ t('traffic_up_down') }}</dt>
            <dd>{{ sizeFormat(row.up) }} / {{ sizeFormat(row.down) }}</dd>
          </div>
          <div>
            <dt>{{ t('stream_settings') }}</dt>
            <dd>{{ streamLabel(row) }}</dd>
          </div>
          <div>
            <dt>{{ t('expiry_time') }}</dt>
            <dd>{{ row.expiryTime > 0 ? formatMillis(row.expiryTime) : t('unlimited') }}</dd>
          </div>
        </dl>
        <footer class="xui-card-row__actions">
          <button
            type="button"
            class="xui-link-btn"
            :aria-label="`${t('view')} — ${row.remark || '#' + row.id}`"
            @click="showInfo(row)"
          >
            {{ t('view') }}
          </button>
          <button
            type="button"
            class="xui-link-btn"
            :aria-label="`${t('client_list')} — ${row.remark || '#' + row.id}`"
            @click="showClients(row)"
          >
            {{ t('client_list') }}
          </button>
          <button
            v-if="row.hasLink()"
            type="button"
            class="xui-link-btn"
            :aria-label="`${t('qrcode')} — ${row.remark || '#' + row.id}`"
            @click="showQrcode(row)"
          >
            {{ t('qrcode') }}
          </button>
          <button
            type="button"
            class="xui-link-btn"
            :aria-label="`${t('edit')} — ${row.remark || '#' + row.id}`"
            @click="openEdit(row)"
          >
            {{ t('edit') }}
          </button>
          <button
            type="button"
            class="xui-link-btn"
            :aria-label="`${t('reset_traffic')} — ${row.remark || '#' + row.id}`"
            @click="resetTraffic(row)"
          >
            {{ t('reset_traffic') }}
          </button>
          <button
            type="button"
            class="xui-link-btn xui-danger-link"
            :aria-label="`${t('delete')} — ${row.remark || '#' + row.id}`"
            @click="remove(row)"
          >
            {{ t('delete') }}
          </button>
        </footer>
      </article>

      <div v-if="!dbInbounds.length" class="xui-empty xui-glass">
        <span class="xui-empty__mark" aria-hidden="true"><InboxOutlined /></span>
        <p class="xui-empty__title">{{ t('inbound_empty') }}</p>
        <p class="xui-empty__hint">{{ t('inbound_empty_hint') }}</p>
        <a-button type="primary" @click="openAdd">
          <template #icon><PlusOutlined /></template>
          {{ t('add_inbound') }}
        </a-button>
      </div>
      <div v-else-if="filterEmpty" class="xui-empty xui-glass">
        <span class="xui-empty__mark" aria-hidden="true"><InboxOutlined /></span>
        <p class="xui-empty__title">{{ t('inbound_filter_empty') }}</p>
      </div>
    </div>

    <div v-else class="xui-panel xui-panel--flush xui-glass xui-table">
      <a-table
        :columns="columns"
        :data-source="visibleInbounds"
        :loading="spinning"
        row-key="id"
        :pagination="false"
        :scroll="{ x: 1150 }"
        size="middle"
      >
        <template #emptyText>
          <div class="xui-empty">
            <span class="xui-empty__mark" aria-hidden="true"><InboxOutlined /></span>
            <p class="xui-empty__title">{{ filterEmpty ? t('inbound_filter_empty') : t('inbound_empty') }}</p>
            <p v-if="!filterEmpty" class="xui-empty__hint">{{ t('inbound_empty_hint') }}</p>
            <a-button v-if="!filterEmpty" type="primary" @click="openAdd">
              <template #icon><PlusOutlined /></template>
              {{ t('add_inbound') }}
            </a-button>
          </div>
        </template>
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'enable'">
            <!--
              开关渲染成一个没有文字的 <button>：列头那个 "enable" 不是它的
              名字，屏幕阅读器读到的只是"按钮"。补一个带备注的标签，一列四个
              开关才分得清哪个是哪个。
            -->
            <a-switch
              :checked="record.enable"
              :aria-label="`${t('enable')} — ${record.remark || '#' + record.id}`"
              @change="(v: any) => { record.enable = !!v; saveRow(record) }"
            />
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
            <a-tag v-if="streamOf(record).transport" color="green">{{ streamOf(record).transport }}</a-tag>
            <a-tag v-if="streamOf(record).tls" color="blue">tls</a-tag>
            <a-tag v-if="streamOf(record).reality" color="purple">reality</a-tag>
            <span v-if="!streamOf(record).transport && !streamOf(record).tls">{{ t('none') }}</span>
          </template>
          <template v-else-if="column.key === 'expiryTime'">
            <a-tag v-if="record.expiryTime > 0" :color="record.isExpiry ? 'red' : 'blue'">
              {{ formatMillis(record.expiryTime) }}
            </a-tag>
            <a-tag v-else color="green">{{ t('unlimited') }}</a-tag>
          </template>
          <template v-else-if="column.key === 'action'">
            <a-space :size="4" wrap>
              <button
                type="button"
                class="xui-link-btn"
                :aria-label="`${t('view')} — ${record.remark || '#' + record.id}`"
                @click="showInfo(record)"
              >
                {{ t('view') }}
              </button>
              <button
                type="button"
                class="xui-link-btn"
                :aria-label="`${t('client_list')} — ${record.remark || '#' + record.id}`"
                @click="showClients(record)"
              >
                {{ t('client_list') }}
              </button>
              <button
                v-if="record.hasLink()"
                type="button"
                class="xui-link-btn"
                :aria-label="`${t('qrcode')} — ${record.remark || '#' + record.id}`"
                @click="showQrcode(record)"
              >
                {{ t('qrcode') }}
              </button>
              <button
                type="button"
                class="xui-link-btn"
                :aria-label="`${t('edit')} — ${record.remark || '#' + record.id}`"
                @click="openEdit(record)"
              >
                {{ t('edit') }}
              </button>
              <button
                type="button"
                class="xui-link-btn"
                :aria-label="`${t('reset_traffic')} — ${record.remark || '#' + record.id}`"
                @click="resetTraffic(record)"
              >
                {{ t('reset_traffic') }}
              </button>
              <button
                type="button"
                class="xui-link-btn xui-danger-link"
                :aria-label="`${t('delete')} — ${record.remark || '#' + record.id}`"
                @click="remove(record)"
              >
                {{ t('delete') }}
              </button>
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

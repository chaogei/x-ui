<script setup lang="ts">
/**
 * StatusView —— 系统状态面板。
 */
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  CloudDownloadOutlined,
  CloudUploadOutlined,
  QuestionCircleFilled,
} from '@ant-design/icons-vue'
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { t } from '../boot'
import { formatSecond, sizeFormat, toFixed } from '../format'
import { post, sleep } from '../http'

interface CurTotal {
  current: number
  total: number
}

interface StatusPayload {
  cpu: number
  mem: CurTotal
  swap: CurTotal
  disk: CurTotal
  loads: number[]
  netIO: { up: number; down: number }
  netTraffic: { sent: number; recv: number }
  tcpCount: number
  udpCount: number
  uptime: number
  core: { state: string; errorMsg: string; version: string }
}

const status = ref<StatusPayload>({
  cpu: 0,
  mem: { current: 0, total: 0 },
  swap: { current: 0, total: 0 },
  disk: { current: 0, total: 0 },
  loads: [0, 0, 0],
  netIO: { up: 0, down: 0 },
  netTraffic: { sent: 0, recv: 0 },
  tcpCount: 0,
  udpCount: 0,
  uptime: 0,
  core: { state: 'stop', errorMsg: '', version: '' },
})

const spinning = ref(false)
const loadingTip = ref('')
const versionOpen = ref(false)
const versions = ref<string[]>([])
let stopped = false

function percent(v: CurTotal): number {
  return v.total === 0 ? 0 : toFixed((v.current / v.total) * 100, 2)
}

function gaugeColor(v: CurTotal): string {
  const p = percent(v)
  if (p < 80) return '#67C23A'
  if (p < 90) return '#E6A23C'
  return '#F56C6C'
}

function coreColor(state: string): string {
  switch (state) {
    case 'running':
      return 'green'
    case 'stop':
      return 'orange'
    case 'error':
      return 'red'
    default:
      return 'default'
  }
}

async function refresh(): Promise<void> {
  const msg = await post<StatusPayload>('server/status', undefined, true)
  if (msg.success && msg.obj) {
    status.value = msg.obj
  }
}

async function openVersions(): Promise<void> {
  spinning.value = true
  loadingTip.value = t('loading')
  const msg = await post<string[]>('server/getCoreVersion')
  spinning.value = false
  if (!msg.success) {
    return
  }
  versions.value = msg.obj ?? []
  versionOpen.value = true
}

async function switchVersion(version: string): Promise<void> {
  versionOpen.value = false
  spinning.value = true
  loadingTip.value = t('installing_core_tip')
  await post(`server/installCore/${version}`)
  spinning.value = false
}

onMounted(async () => {
  while (!stopped) {
    try {
      await refresh()
    } catch {
      // 轮询失败不打断循环：面板重启期间会连续失败几次，属正常。
    }
    await sleep(2000)
  }
})

onBeforeUnmount(() => {
  stopped = true
})
</script>

<template>
  <a-spin :spinning="spinning" :tip="loadingTip">
    <a-card class="xui-card">
      <a-row :gutter="[16, 16]">
        <a-col :xs="12" :md="6" class="xui-gauge">
          <a-progress type="dashboard" :stroke-color="gaugeColor({ current: status.cpu, total: 100 })" :percent="toFixed(status.cpu, 2)" />
          <div>CPU</div>
        </a-col>
        <a-col :xs="12" :md="6" class="xui-gauge">
          <a-progress type="dashboard" :stroke-color="gaugeColor(status.mem)" :percent="percent(status.mem)" />
          <div>{{ t('memory') }}: {{ sizeFormat(status.mem.current) }} / {{ sizeFormat(status.mem.total) }}</div>
        </a-col>
        <a-col :xs="12" :md="6" class="xui-gauge">
          <a-progress type="dashboard" :stroke-color="gaugeColor(status.swap)" :percent="percent(status.swap)" />
          <div>swap: {{ sizeFormat(status.swap.current) }} / {{ sizeFormat(status.swap.total) }}</div>
        </a-col>
        <a-col :xs="12" :md="6" class="xui-gauge">
          <a-progress type="dashboard" :stroke-color="gaugeColor(status.disk)" :percent="percent(status.disk)" />
          <div>{{ t('disk') }}: {{ sizeFormat(status.disk.current) }} / {{ sizeFormat(status.disk.total) }}</div>
        </a-col>
      </a-row>
    </a-card>

    <a-row :gutter="[16, 16]">
      <a-col :xs="24" :md="12">
        <a-card>
          {{ t('sing_box_status') }}:
          <a-tag :color="coreColor(status.core.state)">{{ status.core.state }}</a-tag>
          <a-tooltip v-if="status.core.state === 'error'">
            <template #title>
              <p v-for="(line, i) in (status.core.errorMsg || '').split('\n')" :key="i">{{ line }}</p>
            </template>
            <QuestionCircleFilled />
          </a-tooltip>
          <a-tag color="green" style="cursor: pointer" @click="openVersions">{{ status.core.version }}</a-tag>
          <a-tag color="blue" style="cursor: pointer" @click="openVersions">{{ t('switch_version') }}</a-tag>
        </a-card>
      </a-col>
      <a-col :xs="24" :md="12">
        <a-card>
          {{ t('uptime') }}:
          <a-tag color="#87d068">{{ formatSecond(status.uptime) }}</a-tag>
          <a-tooltip :title="t('uptime_hint')"><QuestionCircleFilled /></a-tooltip>
        </a-card>
      </a-col>
      <a-col :xs="24" :md="12">
        <a-card>{{ t('loads') }}: {{ status.loads.map((l) => toFixed(l, 2)).join(' | ') }}</a-card>
      </a-col>
      <a-col :xs="24" :md="12">
        <a-card>
          {{ t('connections') }}: {{ status.tcpCount }} / {{ status.udpCount }}
          <a-tooltip :title="t('tcp_udp_hint')"><QuestionCircleFilled /></a-tooltip>
        </a-card>
      </a-col>
      <a-col :xs="24" :md="12">
        <a-card>
          <a-row>
            <a-col :span="12">
              <ArrowUpOutlined /> {{ sizeFormat(status.netIO.up) }} / S
              <a-tooltip :title="t('net_io_up_hint')"><QuestionCircleFilled /></a-tooltip>
            </a-col>
            <a-col :span="12">
              <ArrowDownOutlined /> {{ sizeFormat(status.netIO.down) }} / S
              <a-tooltip :title="t('net_io_down_hint')"><QuestionCircleFilled /></a-tooltip>
            </a-col>
          </a-row>
        </a-card>
      </a-col>
      <a-col :xs="24" :md="12">
        <a-card>
          <a-row>
            <a-col :span="12">
              <CloudUploadOutlined /> {{ sizeFormat(status.netTraffic.sent) }}
              <a-tooltip :title="t('net_traffic_sent_hint')"><QuestionCircleFilled /></a-tooltip>
            </a-col>
            <a-col :span="12">
              <CloudDownloadOutlined /> {{ sizeFormat(status.netTraffic.recv) }}
              <a-tooltip :title="t('net_traffic_recv_hint')"><QuestionCircleFilled /></a-tooltip>
            </a-col>
          </a-row>
        </a-card>
      </a-col>
    </a-row>

    <a-modal v-model:open="versionOpen" :title="t('switch_version')" :footer="null">
      <p>{{ t('switch_version_hint') }}</p>
      <p>{{ t('switch_version_warn') }}</p>
      <a-tag
        v-for="(version, index) in versions"
        :key="version"
        :color="index % 2 === 0 ? 'blue' : 'green'"
        style="margin: 6px; cursor: pointer"
        @click="switchVersion(version)"
      >
        {{ version }}
      </a-tag>
    </a-modal>
  </a-spin>
</template>

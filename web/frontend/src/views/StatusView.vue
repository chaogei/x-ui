<script setup lang="ts">
/**
 * StatusView —— 系统状态面板。
 *
 * 版面是两排玻璃瓦片：上排四个仪表（CPU / 内存 / swap / 磁盘），下排是核心、
 * 运行时长、负载、连接数和网络。旧版把后五项塞进一排没有标题的卡片里，只留
 * 一堆彩色 tag，看的人得先猜哪个数字是什么。
 */
import {
  ArrowDownOutlined,
  ArrowUpOutlined,
  CloudDownloadOutlined,
  CloudUploadOutlined,
  QuestionCircleOutlined,
} from '@ant-design/icons-vue'
import { computed, ref } from 'vue'

import { t } from '../boot'
import { formatSecond, sizeFormat, toFixed } from '../format'
import { post } from '../http'
import { usePolling } from '../poll'

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

function percent(v: CurTotal): number {
  return v.total === 0 ? 0 : toFixed((v.current / v.total) * 100, 2)
}

/** 仪表配色是三档阈值，不是渐变：一眼能分出"还好 / 该看看了 / 出事了"。 */
function gaugeColor(p: number): string {
  if (p < 80) return '#34d399'
  if (p < 90) return '#fbbf24'
  return '#fb7185'
}

const gauges = computed(() => [
  {
    key: 'cpu',
    label: t('cpu'),
    percent: toFixed(status.value.cpu, 2),
    detail: '',
  },
  {
    key: 'mem',
    label: t('memory'),
    percent: percent(status.value.mem),
    detail: `${sizeFormat(status.value.mem.current)} / ${sizeFormat(status.value.mem.total)}`,
  },
  {
    key: 'swap',
    label: t('swap'),
    percent: percent(status.value.swap),
    detail: `${sizeFormat(status.value.swap.current)} / ${sizeFormat(status.value.swap.total)}`,
  },
  {
    key: 'disk',
    label: t('disk'),
    percent: percent(status.value.disk),
    detail: `${sizeFormat(status.value.disk.current)} / ${sizeFormat(status.value.disk.total)}`,
  },
])

/** 核心状态的三种取值各自对应一种 chip 配色。 */
const coreChipClass = computed(() => {
  switch (status.value.core.state) {
    case 'running':
      return 'xui-chip xui-chip--ok'
    case 'error':
      return 'xui-chip xui-chip--bad'
    default:
      return 'xui-chip xui-chip--warn'
  }
})

/**
 * refresh 拉一次状态快照。
 *
 * 返回 false 让轮询退避：面板重启期间这里会连着失败十几次，固定 2 秒一发
 * 只是在往一台正在起来的机器上砸请求。post() 已经把网络异常折叠成
 * success:false，所以这里不会抛。
 */
async function refresh(): Promise<boolean> {
  const msg = await post<StatusPayload>('server/status', undefined, true)
  if (!msg.success || !msg.obj) {
    return false
  }
  status.value = msg.obj
  return true
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

usePolling(refresh, { interval: 2000 })
</script>

<template>
  <a-spin :spinning="spinning" :tip="loadingTip">
    <div class="xui-stack">
      <section>
        <h2 class="xui-section-title">{{ t('system_status') }}</h2>
        <div class="xui-tiles">
          <article v-for="g in gauges" :key="g.key" class="xui-tile xui-tile--gauge xui-glass">
            <span class="xui-tile__label">{{ g.label }}</span>
            <div class="xui-tile__gauge">
              <a-progress
                type="dashboard"
                :size="118"
                :stroke-color="gaugeColor(g.percent)"
                trail-color="rgba(255, 255, 255, 0.1)"
                :percent="g.percent"
              />
            </div>
            <p class="xui-tile__meta">{{ g.detail || '\u00a0' }}</p>
          </article>
        </div>
      </section>

      <section>
        <div class="xui-tiles xui-tiles--wide">
          <article class="xui-tile xui-glass">
            <span class="xui-tile__label">{{ t('sing_box_status') }}</span>
            <div class="xui-tile__row">
              <span :class="coreChipClass">{{ status.core.state }}</span>
              <a-tooltip v-if="status.core.state === 'error'">
                <template #title>
                  <p v-for="(line, i) in (status.core.errorMsg || '').split('\n')" :key="i">{{ line }}</p>
                </template>
                <QuestionCircleOutlined />
              </a-tooltip>
              <span v-if="status.core.version" class="xui-chip">{{ status.core.version }}</span>
            </div>
            <div class="xui-tile__row">
              <a-button size="small" @click="openVersions">{{ t('switch_version') }}</a-button>
            </div>
          </article>

          <article class="xui-tile xui-glass">
            <span class="xui-tile__label">{{ t('uptime') }}</span>
            <p class="xui-tile__value">{{ formatSecond(status.uptime) }}</p>
            <p class="xui-tile__meta">{{ t('uptime_hint') }}</p>
          </article>

          <article class="xui-tile xui-glass">
            <span class="xui-tile__label">{{ t('loads') }}</span>
            <p class="xui-tile__value">{{ status.loads.map((l) => toFixed(l, 2)).join('  /  ') }}</p>
            <p class="xui-tile__meta">1 / 5 / 15 min</p>
          </article>

          <article class="xui-tile xui-glass">
            <span class="xui-tile__label">{{ t('connections') }}</span>
            <p class="xui-tile__value">{{ status.tcpCount }} / {{ status.udpCount }}</p>
            <p class="xui-tile__meta">{{ t('tcp_udp_hint') }}</p>
          </article>

          <article class="xui-tile xui-glass">
            <span class="xui-tile__label">{{ t('network_io') }}</span>
            <div class="xui-duo">
              <div class="xui-duo__cell">
                <a-tooltip :title="t('net_io_up_hint')">
                  <span class="xui-duo__cap"><ArrowUpOutlined /> {{ t('upload') }}</span>
                </a-tooltip>
                <span class="xui-tile__value">{{ sizeFormat(status.netIO.up) }}/s</span>
              </div>
              <div class="xui-duo__cell">
                <a-tooltip :title="t('net_io_down_hint')">
                  <span class="xui-duo__cap"><ArrowDownOutlined /> {{ t('download_rate') }}</span>
                </a-tooltip>
                <span class="xui-tile__value">{{ sizeFormat(status.netIO.down) }}/s</span>
              </div>
            </div>
          </article>

          <article class="xui-tile xui-glass">
            <span class="xui-tile__label">{{ t('network_traffic') }}</span>
            <div class="xui-duo">
              <div class="xui-duo__cell">
                <a-tooltip :title="t('net_traffic_sent_hint')">
                  <span class="xui-duo__cap"><CloudUploadOutlined /> {{ t('upload') }}</span>
                </a-tooltip>
                <span class="xui-tile__value">{{ sizeFormat(status.netTraffic.sent) }}</span>
              </div>
              <div class="xui-duo__cell">
                <a-tooltip :title="t('net_traffic_recv_hint')">
                  <span class="xui-duo__cap"><CloudDownloadOutlined /> {{ t('download_rate') }}</span>
                </a-tooltip>
                <span class="xui-tile__value">{{ sizeFormat(status.netTraffic.recv) }}</span>
              </div>
            </div>
          </article>
        </div>
      </section>
    </div>

    <a-modal v-model:open="versionOpen" :title="t('switch_version')" :footer="null">
      <p class="xui-tile__meta">{{ t('switch_version_hint') }}</p>
      <p class="xui-tile__meta">{{ t('switch_version_warn') }}</p>
      <div class="xui-version-list">
        <a-button v-for="version in versions" :key="version" size="small" @click="switchVersion(version)">
          {{ version }}
        </a-button>
      </div>
    </a-modal>
  </a-spin>
</template>

<style scoped>
.xui-version-list {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 16px;
}
</style>

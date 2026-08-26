<script setup lang="ts">
/**
 * InboundInfoModal —— 入站详情，按协议展示凭证与传输层信息。
 */
import { message } from 'ant-design-vue'
import { computed } from 'vue'

import { t } from '../boot'
import type { Inbound } from '../models/core'
import type { DBInbound } from '../models/inbound'

const props = defineProps<{
  open: boolean
  dbInbound: DBInbound | null
  inbound: Inbound | null
}>()

const emit = defineEmits<{ (e: 'update:open', v: boolean): void }>()

const protocol = computed(() => props.dbInbound?.protocol ?? '')
const settings = computed<Record<string, any>>(() => props.inbound?.settings ?? {})
const firstUser = computed<Record<string, any>>(() => settings.value.users?.[0] ?? {})
const firstPeer = computed<Record<string, any>>(() => settings.value.peers?.[0] ?? {})

const isOneOf = (...keys: string[]): boolean => keys.includes(protocol.value)

const link = computed(() => (props.dbInbound?.hasLink() ? props.dbInbound.genLink() : ''))

async function copyLink(): Promise<void> {
  try {
    await navigator.clipboard.writeText(link.value)
    message.success(t('info_copy_success'))
  } catch {
    // 非 HTTPS 或没授权时 clipboard API 不可用，退回到手动选中。
    message.error(t('info_copy_failed'))
  }
}
</script>

<template>
  <a-modal
    :open="open"
    :title="t('detail')"
    :cancel-text="t('close')"
    :ok-text="t('info_copy_link')"
    :ok-button-props="{ style: link ? undefined : { display: 'none' } }"
    @update:open="(v: boolean) => emit('update:open', v)"
    @ok="copyLink"
  >
    <div v-if="dbInbound && inbound" class="xui-kv">
      <p>{{ t('protocol') }}: <a-tag color="green">{{ dbInbound.protocol }}</a-tag></p>
      <p>{{ t('info_address') }}: <a-tag color="blue">{{ dbInbound.address }}</a-tag></p>
      <p>{{ t('port') }}: <a-tag color="green">{{ dbInbound.port }}</a-tag></p>

      <template v-if="protocol === 'vmess'">
        <p>UUID: <a-tag color="green">{{ inbound.uuid }}</a-tag></p>
        <p>alterId: <a-tag color="green">{{ firstUser.alterId || 0 }}</a-tag></p>
      </template>
      <template v-if="protocol === 'vless'">
        <p>UUID: <a-tag color="green">{{ inbound.uuid }}</a-tag></p>
        <p v-if="firstUser.flow">flow: <a-tag color="green">{{ firstUser.flow }}</a-tag></p>
      </template>
      <template v-if="protocol === 'trojan'">
        <p>{{ t('info_password') }}: <a-tag color="green">{{ inbound.password }}</a-tag></p>
      </template>
      <template v-if="protocol === 'shadowsocks'">
        <p>{{ t('info_encryption') }}: <a-tag color="green">{{ inbound.method }}</a-tag></p>
        <p>{{ t('info_password') }}: <a-tag color="green">{{ settings.password }}</a-tag></p>
      </template>
      <template v-if="protocol === 'hysteria2'">
        <p>{{ t('info_password') }}: <a-tag color="green">{{ inbound.password }}</a-tag></p>
        <p>{{ t('info_updown') }}: <a-tag color="green">{{ settings.up_mbps }} / {{ settings.down_mbps }} Mbps</a-tag></p>
      </template>
      <template v-if="protocol === 'tuic'">
        <p>UUID: <a-tag color="green">{{ inbound.uuid }}</a-tag></p>
        <p>{{ t('info_password') }}: <a-tag color="green">{{ inbound.password }}</a-tag></p>
        <p>{{ t('info_congestion') }}: <a-tag color="green">{{ settings.congestion_control }}</a-tag></p>
      </template>
      <!-- AnyTLS/ShadowTLS 的 users[0] 是 {name, password}，没有 username 字段；
           这里展示的是人类可读的备注名，与 socks/http 的认证 username 语义不同。 -->
      <template v-if="isOneOf('anytls', 'shadowtls')">
        <p>{{ t('info_username') }}: <a-tag color="green">{{ firstUser.name || '' }}</a-tag></p>
        <p>{{ t('info_password') }}: <a-tag color="green">{{ inbound.password }}</a-tag></p>
      </template>
      <template v-if="isOneOf('naive', 'socks', 'http', 'mixed')">
        <p>{{ t('info_username') }}: <a-tag color="green">{{ inbound.username }}</a-tag></p>
        <p>{{ t('info_password') }}: <a-tag color="green">{{ inbound.password }}</a-tag></p>
      </template>
      <template v-if="protocol === 'wireguard'">
        <p>peer: <a-tag color="green">{{ firstPeer.address || '' }}:{{ firstPeer.port || '' }}</a-tag></p>
        <p>public_key: <a-tag color="green">{{ firstPeer.public_key || '' }}</a-tag></p>
      </template>

      <template v-if="inbound.transportType">
        <p>{{ t('info_transport') }}: <a-tag color="green">{{ inbound.transportType }}</a-tag></p>
        <p v-if="settings.transport?.path">path: <a-tag color="green">{{ settings.transport.path }}</a-tag></p>
        <p v-if="settings.transport?.host?.length">host: <a-tag color="green">{{ settings.transport.host.join(',') }}</a-tag></p>
        <p v-if="settings.transport?.service_name">serviceName: <a-tag color="green">{{ settings.transport.service_name }}</a-tag></p>
      </template>
      <template v-if="inbound.tls">
        <p>tls: <a-tag color="green">{{ t('info_tls_enabled') }}</a-tag></p>
        <p>SNI: <a-tag :color="inbound.serverName ? 'green' : 'orange'">{{ inbound.serverName || t('none') }}</a-tag></p>
        <template v-if="settings.tls?.reality?.enabled">
          <p>Reality: <a-tag color="purple">{{ t('info_tls_enabled') }}</a-tag></p>
          <p>
            {{ t('info_reality_handshake') }}:
            <a-tag color="green">{{ settings.tls.reality.handshake_server }}:{{ settings.tls.reality.handshake_port }}</a-tag>
          </p>
        </template>
      </template>
      <p v-else-if="inbound.canSniff">tls: <a-tag color="red">{{ t('info_tls_disabled') }}</a-tag></p>
    </div>
  </a-modal>
</template>

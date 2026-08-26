<script setup lang="ts">
/**
 * RealityFields —— VLESS 独有的 Reality 子块。
 *
 * public_key 只用于生成分享链接的 pbk 参数，不会写进 sing-box 配置，但缺了它
 * 客户端无法握手，所以与 private_key 同为必填。「生成密钥对」直接调后端的
 * X25519 生成接口，杜绝手工填写导致的两者错配 —— 那种错配只会在客户端连不上
 * 的时候才暴露，极难排查。
 */
import { t } from '../boot'
import { post } from '../http'
import { RealityBlock, type TlsBlock } from '../models/core'

const props = defineProps<{ tls: TlsBlock }>()

function toggle(on: boolean): void {
  if (!props.tls.reality) {
    props.tls.reality = new RealityBlock()
  }
  props.tls.reality.enabled = on
}

function setShortId(raw: string): void {
  // 这里刻意保留空串条目：Reality 的 short_id 允许 "" 表示不校验。
  props.tls.reality!.short_id = (raw || '').split(',').map((s) => s.trim())
}

async function genKeyPair(): Promise<void> {
  const msg = await post<{ private_key: string; public_key: string }>('xui/api/reality/keypair')
  if (!msg.success || !msg.obj) {
    return
  }
  if (!props.tls.reality) {
    props.tls.reality = new RealityBlock(true)
  }
  props.tls.reality.private_key = msg.obj.private_key
  props.tls.reality.public_key = msg.obj.public_key
}
</script>

<template>
  <a-divider>Reality</a-divider>
  <div class="xui-form-grid">
    <a-form-item :label="t('proto_reality_enable')">
      <a-switch :checked="!!tls.reality?.enabled" @change="(v: any) => toggle(!!v)" />
    </a-form-item>
    <template v-if="tls.reality?.enabled">
      <a-form-item label="handshake server">
        <a-input v-model:value="tls.reality.handshake_server" :placeholder="t('proto_reality_sni_ph')" />
      </a-form-item>
      <a-form-item label="handshake port">
        <a-input-number v-model:value="tls.reality.handshake_port" />
      </a-form-item>
      <a-form-item label="private_key">
        <a-input v-model:value="tls.reality.private_key" />
      </a-form-item>
      <a-form-item label="public_key">
        <a-input v-model:value="tls.reality.public_key" />
      </a-form-item>
      <a-form-item :label="t('proto_short_id')">
        <a-input :value="tls.reality.short_id.join(',')" @change="(e: any) => setShortId(e.target.value)" />
      </a-form-item>
      <a-form-item>
        <a-button type="primary" size="small" @click="genKeyPair">{{ t('proto_reality_gen_keys') }}</a-button>
      </a-form-item>
    </template>
  </div>
</template>

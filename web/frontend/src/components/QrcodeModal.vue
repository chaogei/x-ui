<script setup lang="ts">
/**
 * QrcodeModal —— 把一条分享链接/订阅地址渲染成二维码。
 */
import { message } from 'ant-design-vue'
import QRCode from 'qrcode'
import { ref, watch } from 'vue'

import { t } from '../boot'

const props = defineProps<{
  open: boolean
  title: string
  text: string
}>()

const emit = defineEmits<{ (e: 'update:open', v: boolean): void }>()

const dataUrl = ref('')

watch(
  () => [props.open, props.text],
  async () => {
    if (!props.open || !props.text) {
      dataUrl.value = ''
      return
    }
    try {
      dataUrl.value = await QRCode.toDataURL(props.text, { width: 320, margin: 1 })
    } catch {
      // 链接过长时 QR 容量不够，此时只显示文本。
      dataUrl.value = ''
    }
  },
  { immediate: true },
)

async function copy(): Promise<void> {
  try {
    await navigator.clipboard.writeText(props.text)
    message.success(t('info_copy_success'))
  } catch {
    message.error(t('info_copy_failed'))
  }
}
</script>

<template>
  <a-modal
    :open="open"
    :title="title"
    :footer="null"
    @update:open="(v: boolean) => emit('update:open', v)"
  >
    <div style="text-align: center">
      <img v-if="dataUrl" :src="dataUrl" :alt="title" style="max-width: 100%" />
      <a-typography-paragraph copyable :content="text" style="word-break: break-all; margin-top: 12px" />
      <a-button @click="copy">{{ t('info_copy_link') }}</a-button>
    </div>
  </a-modal>
</template>

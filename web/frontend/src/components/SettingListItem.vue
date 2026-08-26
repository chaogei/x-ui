<script setup lang="ts">
/**
 * SettingListItem —— 设置页里"标题 + 说明 + 一个控件"的一行。
 */
defineProps<{
  type: 'text' | 'number' | 'textarea' | 'switch' | 'password'
  title: string
  desc?: string
  value: string | number | boolean
}>()

const emit = defineEmits<{ (e: 'update:value', v: string | number | boolean): void }>()
</script>

<template>
  <a-list-item style="padding: 16px 20px">
    <a-row style="width: 100%" :gutter="[16, 8]">
      <a-col :xs="24" :xl="12">
        <a-list-item-meta :title="title" :description="desc" />
      </a-col>
      <a-col :xs="24" :xl="12">
        <a-input
          v-if="type === 'text'"
          :value="value as string"
          @change="(e: any) => emit('update:value', e.target.value)"
        />
        <a-input-password
          v-else-if="type === 'password'"
          :value="value as string"
          @change="(e: any) => emit('update:value', e.target.value)"
        />
        <a-input-number
          v-else-if="type === 'number'"
          :value="value as number"
          style="width: 100%"
          @change="(v: any) => emit('update:value', Number(v) || 0)"
        />
        <a-textarea
          v-else-if="type === 'textarea'"
          :value="value as string"
          :rows="10"
          @change="(e: any) => emit('update:value', e.target.value)"
        />
        <a-switch v-else-if="type === 'switch'" :checked="!!value" @change="(v: any) => emit('update:value', !!v)" />
      </a-col>
    </a-row>
  </a-list-item>
</template>

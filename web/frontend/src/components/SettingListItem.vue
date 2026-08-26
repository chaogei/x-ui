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
  <!--
    这里是 <div> 而不是 <a-list-item>。后者渲染成 <li>，但 a-list 只有在拿到
    data-source 时才会生成 <ul>；设置页是把行当插槽内容写死的，于是那些 <li>
    落在一个普通 <div> 里 —— 一个没有列表的列表项，辅助技术会把整页设置的结构
    报错。类名照旧，样式一个字都没变。
  -->
  <div class="ant-list-item" style="padding: 16px 20px">
    <a-row style="width: 100%" :gutter="[16, 8]">
      <a-col :xs="24" :xl="12">
        <a-list-item-meta :title="title" :description="desc" />
      </a-col>
      <!--
        标题在左边那一列，与控件之间没有 <label for>，所以每个控件都要自己带
        名字 —— 否则屏幕阅读器把设置页念成一串没有标题的输入框。
      -->
      <a-col :xs="24" :xl="12">
        <a-input
          v-if="type === 'text'"
          :value="value as string"
          :aria-label="title"
          @change="(e: any) => emit('update:value', e.target.value)"
        />
        <a-input-password
          v-else-if="type === 'password'"
          :value="value as string"
          :aria-label="title"
          @change="(e: any) => emit('update:value', e.target.value)"
        />
        <a-input-number
          v-else-if="type === 'number'"
          :value="value as number"
          :aria-label="title"
          style="width: 100%"
          @change="(v: any) => emit('update:value', Number(v) || 0)"
        />
        <a-textarea
          v-else-if="type === 'textarea'"
          :value="value as string"
          :rows="10"
          :aria-label="title"
          @change="(e: any) => emit('update:value', e.target.value)"
        />
        <a-switch
          v-else-if="type === 'switch'"
          :checked="!!value"
          :aria-label="title"
          @change="(v: any) => emit('update:value', !!v)"
        />
      </a-col>
    </a-row>
  </div>
</template>

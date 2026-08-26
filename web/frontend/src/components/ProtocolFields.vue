<script setup lang="ts">
/**
 * ProtocolFields —— 按 forms.ts 的描述渲染一组协议字段。
 */
import { t } from '../boot'
import { getPath, setPath, type Field } from '../models/forms'

const props = defineProps<{
  settings: Record<string, any>
  fields: Field[]
}>()

function read(path: string): any {
  return getPath(props.settings, path)
}

function write(path: string, value: unknown): void {
  setPath(props.settings, path, value)
}

/** csv 字段在界面上是一行逗号分隔文本，在模型里是字符串数组。 */
function readCsv(path: string): string {
  const v = read(path)
  return Array.isArray(v) ? v.join(',') : (v ?? '')
}

function writeCsv(path: string, raw: string, keepEmpty = false): void {
  const parts = (raw || '').split(',').map((s) => s.trim())
  write(path, keepEmpty ? parts : parts.filter(Boolean))
}

/** label 里已经是字面量（UUID、MTU）时原样显示，否则查词典。 */
function label(key: string): string {
  return t(key)
}
</script>

<template>
  <div class="xui-form-grid">
    <template v-for="field in fields" :key="field.path">
      <a-form-item :label="label(field.label)">
        <a-input
          v-if="field.kind === 'text'"
          :value="read(field.path)"
          :placeholder="field.placeholder ? t(field.placeholder) : undefined"
          @change="(e: any) => write(field.path, (e.target.value ?? '').trim())"
        />
        <a-input-number
          v-else-if="field.kind === 'number'"
          :value="read(field.path)"
          @change="(v: any) => write(field.path, Number(v) || 0)"
        />
        <a-switch
          v-else-if="field.kind === 'switch'"
          :checked="!!read(field.path)"
          @change="(v: any) => write(field.path, !!v)"
        />
        <a-select
          v-else-if="field.kind === 'select'"
          :value="read(field.path)"
          :style="{ width: (field.width ?? 160) + 'px' }"
          @change="(v: any) => write(field.path, v)"
        >
          <a-select-option v-for="opt in field.options" :key="String(opt.value)" :value="opt.value">
            {{ t(opt.label) }}
          </a-select-option>
        </a-select>
        <a-input
          v-else-if="field.kind === 'csv'"
          :value="readCsv(field.path)"
          :placeholder="field.placeholder ? t(field.placeholder) : undefined"
          @change="(e: any) => writeCsv(field.path, e.target.value, field.keepEmpty)"
        />
        <a-textarea
          v-else-if="field.kind === 'textarea'"
          :value="read(field.path)"
          :rows="field.rows ?? 3"
          :placeholder="field.placeholder ? t(field.placeholder) : undefined"
          @change="(e: any) => write(field.path, e.target.value)"
        />
      </a-form-item>
    </template>
  </div>
</template>

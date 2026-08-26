/**
 * format.ts —— 展示层格式化。
 *
 * 这些函数以前散在 assets/js/util/{common,date-util}.js 里，
 * 其中 formatSecond 的单位名是硬编码简体中文，英文界面上会中英混排；
 * 现在统一走 i18n 词典。
 */
import dayjs from 'dayjs'

import { t } from './boot'

export const ONE_KB = 1024
export const ONE_MB = ONE_KB * 1024
export const ONE_GB = ONE_MB * 1024
export const ONE_TB = ONE_GB * 1024
export const ONE_PB = ONE_TB * 1024

export function toFixed(num: number, n: number): number {
  const p = Math.pow(10, n)
  return Math.round(num * p) / p
}

export function sizeFormat(size: number): string {
  const v = Number(size) || 0
  if (v < ONE_KB) {
    return v.toFixed(0) + ' B'
  }
  if (v < ONE_MB) {
    return (v / ONE_KB).toFixed(2) + ' KB'
  }
  if (v < ONE_GB) {
    return (v / ONE_MB).toFixed(2) + ' MB'
  }
  if (v < ONE_TB) {
    return (v / ONE_GB).toFixed(2) + ' GB'
  }
  if (v < ONE_PB) {
    return (v / ONE_TB).toFixed(2) + ' TB'
  }
  return (v / ONE_PB).toFixed(2) + ' PB'
}

export function formatSecond(second: number): string {
  const v = Number(second) || 0
  if (v < 60) {
    return `${v.toFixed(0)} ${t('unit_second')}`
  }
  if (v < 3600) {
    return `${(v / 60).toFixed(0)} ${t('unit_minute')}`
  }
  if (v < 3600 * 24) {
    return `${(v / 3600).toFixed(0)} ${t('unit_hour')}`
  }
  return `${(v / 3600 / 24).toFixed(0)} ${t('unit_day')}`
}

/** formatMillis 把毫秒时间戳渲染成本地时间；0 视为"无"。 */
export function formatMillis(millis: number): string {
  if (!millis) {
    return ''
  }
  return dayjs(millis).format('YYYY-MM-DD HH:mm:ss')
}

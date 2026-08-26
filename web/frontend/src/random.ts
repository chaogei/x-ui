/**
 * random.ts —— 生成默认凭证用的随机串。
 *
 * 一律走 crypto.getRandomValues，不用 Math.random。这些值会变成 UUID、
 * Shadowsocks 口令、Trojan 口令——即便面板同时也允许用户自己填，
 * 默认值可预测就等于给每个"点了确定就走"的用户发了一把弱钥匙。
 */

const SEQ = 'abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ'

/** randomInt 返回 [0, n) 上的均匀随机整数。 */
function randomInt(n: number): number {
  // 拒绝采样消除取模偏置：2^32 不是 62 的整数倍，直接 % 会让前几个字符更常出现。
  const limit = Math.floor(0xffffffff / n) * n
  const buf = new Uint32Array(1)
  for (;;) {
    crypto.getRandomValues(buf)
    if (buf[0] < limit) {
      return buf[0] % n
    }
  }
}

export function randomIntRange(min: number, max: number): number {
  return min + randomInt(max - min)
}

export function randomSeq(count: number): string {
  let out = ''
  for (let i = 0; i < count; i++) {
    out += SEQ[randomInt(SEQ.length)]
  }
  return out
}

export function randomUUID(): string {
  if (typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  // 老浏览器兜底：仍然用 CSPRNG 填充，只是自己拼版本位。
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

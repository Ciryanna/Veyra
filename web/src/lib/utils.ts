// 轻量 cn：不引 clsx/tailwind-merge，布尔过滤就够本项目用。
export function cn(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(' ')
}

export function fmtNum(n: number): string {
  return n.toLocaleString('zh-CN')
}

export function fmtTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k'
  return String(n)
}

export function fmtTime(ms: number | null | undefined): string {
  if (!ms) return '—'
  return new Date(ms).toLocaleString('zh-CN', { hour12: false })
}

export function fmtMs(n: number): string {
  if (n >= 1000) return (n / 1000).toFixed(1) + 's'
  return n + 'ms'
}

// 成本展示：$0.00247 级精度保留 5 位有效；0 显示为 "—"（未定价不估算）。
export function fmtUSD(n: number | null | undefined): string {
  if (!n) return '—'
  if (n >= 1000) return '$' + n.toLocaleString('zh-CN', { maximumFractionDigits: 0 })
  if (n >= 1) return '$' + n.toFixed(2)
  const s = n.toPrecision(3)
  return '$' + s.replace(/\.?0+$/, '')
}

// 单价展示（USD/Mtok）：0 显示为 "—"。
export function fmtPrice(n: number | null | undefined): string {
  if (!n) return '—'
  return '$' + String(n)
}

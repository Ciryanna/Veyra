import { useCallback, useEffect, useState } from 'react'
import { api, type Stats, type UsageRow } from '../api'
import { Badge, Card, CardContent, CardHeader, CardTitle, EmptyRow, Select } from '../components/ui'
import { cn, fmtMs, fmtNum, fmtTokens, fmtUSD } from '../lib/utils'

const WINDOWS: Array<{ value: string; label: string }> = [
  { value: '', label: '全部' },
  { value: '1h', label: '近 1 小时' },
  { value: '24h', label: '近 24 小时' },
  { value: '7d', label: '近 7 天' },
  { value: '30d', label: '近 30 天' },
]

export default function Dashboard({ onError }: { onError: (e: unknown) => void }) {
  const [win, setWin] = useState('24h')
  const [by, setBy] = useState<'vkey' | 'provider'>('vkey')
  const [stats, setStats] = useState<Stats | null>(null)
  const [rows, setRows] = useState<UsageRow[]>([])

  const load = useCallback(async () => {
    const q = win ? `?since=${win}` : ''
    try {
      setStats(await api<Stats>('GET', `/admin/api/stats${q}`))
      setRows(await api<UsageRow[]>('GET', `/admin/api/usage?by=${by}${win ? `&since=${win}` : ''}`))
    } catch (e) {
      onError(e)
    }
  }, [win, by, onError])

  useEffect(() => {
    load()
    const t = setInterval(load, 15_000) // 仪表盘轮询刷新
    return () => clearInterval(t)
  }, [load])

  const errorRate =
    stats && stats.requests > 0 ? ((stats.errors / stats.requests) * 100).toFixed(1) + '%' : '—'

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold tracking-tight">用量总览</h1>
        <div className="flex gap-2">
          <Select className="w-32" value={win} onChange={(e) => setWin(e.target.value)}>
            {WINDOWS.map((w) => (
              <option key={w.value} value={w.value}>
                {w.label}
              </option>
            ))}
          </Select>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 xl:grid-cols-6">
        <StatCard title="请求数" value={stats ? fmtNum(stats.requests) : '—'} />
        <StatCard title="成本" value={stats ? fmtUSD(stats.cost_usd) : '—'} hint="按记账时价格锁定" />
        <StatCard title="输入 tokens" value={stats ? fmtTokens(stats.input_tokens) : '—'} />
        <StatCard title="输出 tokens" value={stats ? fmtTokens(stats.output_tokens) : '—'} />
        <StatCard
          title="活跃应用"
          value={stats ? fmtNum(stats.active_apps) : '—'}
          hint="窗口内有调用"
        />
        <StatCard
          title="错误率"
          value={errorRate}
          hint={stats ? `${fmtNum(stats.errors)} 次失败` : undefined}
          tone={stats && stats.errors > 0 ? 'warning' : 'default'}
        />
      </div>

      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle>用量明细</CardTitle>
          <div className="flex gap-1 rounded-md bg-secondary p-0.5">
            {(['vkey', 'provider'] as const).map((d) => (
              <button
                key={d}
                onClick={() => setBy(d)}
                className={cn(
                  'rounded px-3 py-1 text-xs transition-colors',
                  by === d ? 'bg-card font-medium shadow-[var(--shadow-sm)]' : 'text-muted-foreground',
                )}
              >
                {d === 'vkey' ? '按应用' : '按上游'}
              </button>
            ))}
          </div>
        </CardHeader>
        <CardContent>
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b text-left text-xs text-muted-foreground">
                <th className="pb-2 font-medium">{by === 'vkey' ? '应用' : '上游'}</th>
                <th className="pb-2 text-right font-medium">请求</th>
                <th className="pb-2 text-right font-medium">输入 tok</th>
                <th className="pb-2 text-right font-medium">输出 tok</th>
                <th className="pb-2 text-right font-medium">错误</th>
                <th className="pb-2 text-right font-medium">成本</th>
                <th className="pb-2 text-right font-medium">平均耗时</th>
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 && <EmptyRow colSpan={7} text="窗口内暂无调用" />}
              {rows.map((r) => (
                <tr key={r.label} className="border-b border-border/50 last:border-0">
                  <td className="py-2.5 font-medium">{r.label}</td>
                  <td className="py-2.5 text-right tabular-nums">{fmtNum(r.requests)}</td>
                  <td className="py-2.5 text-right tabular-nums text-muted-foreground">
                    {fmtTokens(r.input_tokens)}
                  </td>
                  <td className="py-2.5 text-right tabular-nums text-muted-foreground">
                    {fmtTokens(r.output_tokens)}
                  </td>
                  <td className="py-2.5 text-right">
                    {r.errors > 0 ? <Badge tone="destructive">{fmtNum(r.errors)}</Badge> : '0'}
                  </td>
                  <td className="py-2.5 text-right tabular-nums text-muted-foreground">
                    {fmtUSD(r.cost_usd)}
                  </td>
                  <td className="py-2.5 text-right tabular-nums text-muted-foreground">
                    {fmtMs(r.avg_duration_ms)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </CardContent>
      </Card>
    </div>
  )
}

function StatCard({
  title,
  value,
  hint,
  tone = 'default',
}: {
  title: string
  value: string
  hint?: string
  tone?: 'default' | 'warning'
}) {
  return (
    <Card>
      <CardContent className="p-4">
        <p className="text-xs text-muted-foreground">{title}</p>
        <p
          className={cn(
            'mt-1 text-2xl font-semibold tabular-nums tracking-tight',
            tone === 'warning' && 'text-warning',
          )}
        >
          {value}
        </p>
        {hint && <p className="mt-1 text-xs text-muted-foreground/70">{hint}</p>}
      </CardContent>
    </Card>
  )
}

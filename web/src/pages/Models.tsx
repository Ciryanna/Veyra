import { useCallback, useEffect, useState } from 'react'
import { api, type ModelRow, type ProviderRow } from '../api'
import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  EmptyRow,
  Field,
  Input,
  Select,
} from '../components/ui'
import { fmtPrice } from '../lib/utils'

// 价格输入框的字符串态转数字：空/非法 = 0（后端"非零即更新"，
// 0 即保留原价）。负数拒绝提交。
function parsePrice(s: string): number | null {
  if (!s.trim()) return 0
  const n = Number(s)
  if (!Number.isFinite(n) || n < 0) return null
  return n
}

export default function Models({ onError }: { onError: (e: unknown) => void }) {
  const [rows, setRows] = useState<ModelRow[]>([])
  const [providers, setProviders] = useState<ProviderRow[]>([])
  const [provider, setProvider] = useState('')
  const [name, setName] = useState('')
  const [upstream, setUpstream] = useState('')
  const [priceIn, setPriceIn] = useState('')
  const [priceOut, setPriceOut] = useState('')
  const [err, setErr] = useState('')

  const load = useCallback(async () => {
    try {
      setRows(await api<ModelRow[]>('GET', '/admin/api/models'))
      const ps = await api<ProviderRow[]>('GET', '/admin/api/providers')
      setProviders(ps)
      if (!provider && ps.length > 0) setProvider(ps[0].name)
    } catch (e) {
      onError(e)
    }
  }, [onError, provider])

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function save() {
    setErr('')
    if (!provider || !name.trim()) {
      setErr('provider 与 name 必填')
      return
    }
    const inPrice = parsePrice(priceIn)
    const outPrice = parsePrice(priceOut)
    if (inPrice === null || outPrice === null) {
      setErr('价格需为非负数字（USD/百万 token）')
      return
    }
    try {
      await api('PUT', '/admin/api/models', {
        provider,
        name: name.trim(),
        upstream_model: upstream.trim(),
        price_in_per_mtok: inPrice,
        price_out_per_mtok: outPrice,
      })
      setName('')
      setUpstream('')
      setPriceIn('')
      setPriceOut('')
      await load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  async function remove(m: ModelRow) {
    try {
      await api(
        'DELETE',
        `/admin/api/models?provider=${encodeURIComponent(m.provider)}&name=${encodeURIComponent(m.name)}`,
      )
      await load()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-lg font-semibold tracking-tight">模型登记</h1>

      <Card>
        <CardHeader>
          <CardTitle>登记模型</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid gap-4 md:grid-cols-3 xl:grid-cols-6">
            <Field label="Provider">
              <Select value={provider} onChange={(e) => setProvider(e.target.value)}>
                {providers.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="对外模型名" hint="不含 provider 前缀">
              <Input
                value={name}
                placeholder="deepseek-chat"
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Field label="上游模型名" hint="留空 = 与对外名相同">
              <Input
                value={upstream}
                placeholder="（默认同名）"
                onChange={(e) => setUpstream(e.target.value)}
              />
            </Field>
            <Field label="输入价 $/Mtok" hint="留空 = 不改价；0 = 未定价">
              <Input
                value={priceIn}
                placeholder="0.27"
                inputMode="decimal"
                onChange={(e) => setPriceIn(e.target.value)}
              />
            </Field>
            <Field label="输出价 $/Mtok" hint="Jev 类输出免费">
              <Input
                value={priceOut}
                placeholder="1.10"
                inputMode="decimal"
                onChange={(e) => setPriceOut(e.target.value)}
              />
            </Field>
            <div className="flex items-end">
              <Button onClick={save}>登记</Button>
            </div>
          </div>
          {err && <p className="mt-3 text-xs text-destructive">{err}</p>}
          <p className="mt-3 text-xs text-muted-foreground/70">
            登记册不是白名单：未登记的 "provider/模型名" 仍会透传；
            登记提供 /v1/models 列表、改名映射与成本核算（价格在记账时锁定，改价不改历史）。
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>已登记（{rows.length}）</CardTitle>
        </CardHeader>
        <CardContent>
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b text-left text-xs text-muted-foreground">
                <th className="pb-2 font-medium">MODEL</th>
                <th className="pb-2 font-medium">上游名</th>
                <th className="pb-2 text-right font-medium">输入 $/M</th>
                <th className="pb-2 text-right font-medium">输出 $/M</th>
                <th className="pb-2 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 && <EmptyRow colSpan={5} text="尚未登记任何模型" />}
              {rows.map((m) => (
                <tr
                  key={`${m.provider}/${m.name}`}
                  className="border-b border-border/50 last:border-0"
                >
                  <td className="py-2.5 font-medium">
                    {m.provider}/<span className="text-primary">{m.name}</span>
                  </td>
                  <td className="py-2.5 text-muted-foreground">{m.upstream_model}</td>
                  <td className="py-2.5 text-right tabular-nums">
                    {fmtPrice(m.price_in_per_mtok)}
                  </td>
                  <td className="py-2.5 text-right tabular-nums">
                    {fmtPrice(m.price_out_per_mtok)}
                  </td>
                  <td className="py-2.5 text-right">
                    <Button
                      size="sm"
                      variant="ghost"
                      className="text-destructive"
                      onClick={() => remove(m)}
                    >
                      删除
                    </Button>
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

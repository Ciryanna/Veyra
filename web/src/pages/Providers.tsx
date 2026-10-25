import { useCallback, useEffect, useState } from 'react'
import { api, type ProviderInput, type ProviderRow } from '../api'
import {
  Badge,
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
import { fmtTime } from '../lib/utils'

const EMPTY: ProviderInput = {
  name: '',
  kind: 'openai-compat',
  base_url: '',
  api_key: '',
  region: 'domestic',
}

export default function Providers({ onError }: { onError: (e: unknown) => void }) {
  const [rows, setRows] = useState<ProviderRow[]>([])
  const [form, setForm] = useState<ProviderInput>(EMPTY)
  const [editing, setEditing] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      setRows(await api<ProviderRow[]>('GET', '/admin/api/providers'))
    } catch (e) {
      onError(e)
    }
  }, [onError])

  useEffect(() => {
    load()
  }, [load])

  async function save() {
    setErr('')
    if (!form.name.trim() || !form.base_url.trim()) {
      setErr('name 与 base_url 必填')
      return
    }
    setBusy(true)
    try {
      await api('PUT', '/admin/api/providers', form)
      setForm(EMPTY)
      setEditing(false)
      await load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }

  function startEdit(p: ProviderRow) {
    setEditing(true)
    setErr('')
    // 编辑时不回填 key（服务端保留原 key），也允许主动换 key。
    setForm({
      name: p.name,
      kind: p.kind,
      base_url: p.base_url,
      api_key: '',
      region: p.region,
    })
  }

  async function toggle(p: ProviderRow) {
    try {
      await api('PATCH', '/admin/api/providers/enabled', { name: p.name, enabled: !p.enabled })
      await load()
    } catch (e) {
      onError(e)
    }
  }

  async function remove(p: ProviderRow) {
    if (!confirm(`删除 provider "${p.name}" 及其模型登记？`)) return
    try {
      await api('DELETE', `/admin/api/providers?name=${encodeURIComponent(p.name)}`)
      await load()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-lg font-semibold tracking-tight">Providers</h1>

      <Card>
        <CardHeader>
          <CardTitle>{editing ? `编辑：${form.name}` : '登记上游'}</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            <Field label="名称（路由前缀）" hint="请求 model 写作 name/model">
              <Input
                value={form.name}
                disabled={editing}
                placeholder="deepseek"
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </Field>
            <Field label="类型">
              <Select
                value={form.kind}
                onChange={(e) => setForm({ ...form, kind: e.target.value })}
              >
                <option value="openai-compat">openai-compat</option>
                <option value="systemone">systemone (Jev)</option>
                <option value="anthropic">anthropic (/v1/messages)</option>
                <option value="gemini">gemini (原生)</option>
              </Select>
            </Field>
            <Field label="区域" hint="M3 级联路由依据">
              <Select
                value={form.region}
                onChange={(e) => setForm({ ...form, region: e.target.value })}
              >
                <option value="domestic">国内</option>
                <option value="overseas">国外</option>
              </Select>
            </Field>
            <Field
              label="Base URL"
              hint="与 OpenAI SDK base_url 一致，通常含 /v1"
            >
              <Input
                value={form.base_url}
                placeholder="https://api.deepseek.com/v1"
                onChange={(e) => setForm({ ...form, base_url: e.target.value })}
              />
            </Field>
            <Field
              label="API Key"
              hint={editing ? '留空 = 保留原 key' : 'BYOK 密钥，保存后不再可见'}
            >
              <Input
                type="password"
                value={form.api_key}
                placeholder={editing ? '（不修改则留空）' : 'sk-...'}
                onChange={(e) => setForm({ ...form, api_key: e.target.value })}
              />
            </Field>
            <div className="flex items-end gap-2">
              <Button onClick={save} disabled={busy}>
                {editing ? '保存修改' : '登记'}
              </Button>
              {editing && (
                <Button
                  variant="ghost"
                  onClick={() => {
                    setEditing(false)
                    setForm(EMPTY)
                    setErr('')
                  }}
                >
                  取消
                </Button>
              )}
            </div>
          </div>
          {err && <p className="mt-3 text-xs text-destructive">{err}</p>}
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
                <th className="pb-2 font-medium">名称</th>
                <th className="pb-2 font-medium">类型</th>
                <th className="pb-2 font-medium">区域</th>
                <th className="pb-2 font-medium">Base URL</th>
                <th className="pb-2 font-medium">Key</th>
                <th className="pb-2 font-medium">状态</th>
                <th className="pb-2 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 && <EmptyRow colSpan={7} text="尚未登记任何上游" />}
              {rows.map((p) => (
                <tr key={p.name} className="border-b border-border/50 last:border-0">
                  <td className="py-2.5 font-medium">{p.name}</td>
                  <td className="py-2.5 text-muted-foreground">{p.kind}</td>
                  <td className="py-2.5">
                    <Badge tone={p.region === 'overseas' ? 'primary' : 'muted'}>
                      {p.region === 'overseas' ? '国外' : '国内'}
                    </Badge>
                  </td>
                  <td className="max-w-64 truncate py-2.5 text-muted-foreground" title={p.base_url}>
                    {p.base_url}
                  </td>
                  <td className="py-2.5 font-mono text-xs text-muted-foreground">
                    {p.api_key_masked}
                  </td>
                  <td className="py-2.5">
                    <Badge tone={p.enabled ? 'success' : 'warning'}>
                      {p.enabled ? '启用' : '禁用'}
                    </Badge>
                  </td>
                  <td className="py-2.5 text-right">
                    <div className="inline-flex gap-1">
                      <Button size="sm" variant="ghost" onClick={() => startEdit(p)}>
                        编辑
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => toggle(p)}>
                        {p.enabled ? '禁用' : '启用'}
                      </Button>
                      <Button size="sm" variant="ghost" className="text-destructive" onClick={() => remove(p)}>
                        删除
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {rows.length > 0 && (
            <p className="mt-3 text-xs text-muted-foreground/70">
              最后更新 {fmtTime(Math.max(...rows.map((r) => r.updated_at)))}
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

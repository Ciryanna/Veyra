import { useCallback, useEffect, useState } from 'react'
import { api, type VKeyCreated, type VKeyRow } from '../api'
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
} from '../components/ui'
import { fmtTime } from '../lib/utils'

export default function VKeys({ onError }: { onError: (e: unknown) => void }) {
  const [rows, setRows] = useState<VKeyRow[]>([])
  const [app, setApp] = useState('')
  const [err, setErr] = useState('')
  // 一次性明文 key：签发响应离开服务端后永不可再见，只在本地 state 展示。
  const [created, setCreated] = useState<VKeyCreated | null>(null)

  const load = useCallback(async () => {
    try {
      setRows(await api<VKeyRow[]>('GET', '/admin/api/vkeys'))
    } catch (e) {
      onError(e)
    }
  }, [onError])

  useEffect(() => {
    load()
  }, [load])

  async function create() {
    setErr('')
    if (!app.trim()) {
      setErr('应用名必填')
      return
    }
    try {
      const c = await api<VKeyCreated>('POST', '/admin/api/vkeys', { app_name: app.trim() })
      setCreated(c)
      setApp('')
      await load()
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e))
    }
  }

  async function toggle(v: VKeyRow) {
    try {
      await api('PATCH', '/admin/api/vkeys/enabled', { app_name: v.app_name, enabled: !v.enabled })
      await load()
    } catch (e) {
      onError(e)
    }
  }

  async function remove(v: VKeyRow) {
    if (!confirm(`删除应用 "${v.app_name}" 的 key？其所有调用将立即失效。`)) return
    try {
      await api('DELETE', `/admin/api/vkeys?app=${encodeURIComponent(v.app_name)}`)
      await load()
    } catch (e) {
      onError(e)
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <h1 className="text-lg font-semibold tracking-tight">虚拟 Key</h1>

      {created && (
        <Card className="border-primary/50">
          <CardHeader>
            <CardTitle className="text-primary">
              已为 "{created.app_name}" 签发新 key —— 只显示这一次
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <code className="block break-all rounded-md bg-secondary px-4 py-3 font-mono text-sm">
              {created.key}
            </code>
            <div className="flex gap-2">
              <Button size="sm" variant="secondary" onClick={() => navigator.clipboard.writeText(created.key)}>
                复制
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setCreated(null)}>
                我已保存，关闭
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      <Card>
        <CardHeader>
          <CardTitle>签发</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="flex items-end gap-3">
            <div className="w-64">
              <Field label="应用名" hint="同名重签 = 重置凭证（旧 key 立即失效）">
                <Input
                  value={app}
                  placeholder="qa-bot"
                  onChange={(e) => setApp(e.target.value)}
                  onKeyDown={(e) => e.key === 'Enter' && create()}
                />
              </Field>
            </div>
            <Button onClick={create}>签发</Button>
          </div>
          {err && <p className="mt-3 text-xs text-destructive">{err}</p>}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>已签发（{rows.length}）</CardTitle>
        </CardHeader>
        <CardContent>
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b text-left text-xs text-muted-foreground">
                <th className="pb-2 font-medium">应用</th>
                <th className="pb-2 font-medium">Key 前缀</th>
                <th className="pb-2 font-medium">状态</th>
                <th className="pb-2 font-medium">签发时间</th>
                <th className="pb-2 font-medium">最后使用</th>
                <th className="pb-2 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {rows.length === 0 && <EmptyRow colSpan={6} text="尚未为任何应用签发 key" />}
              {rows.map((v) => (
                <tr key={v.app_name} className="border-b border-border/50 last:border-0">
                  <td className="py-2.5 font-medium">{v.app_name}</td>
                  <td className="py-2.5 font-mono text-xs text-muted-foreground">{v.key_prefix}…</td>
                  <td className="py-2.5">
                    <Badge tone={v.enabled ? 'success' : 'warning'}>
                      {v.enabled ? '启用' : '禁用'}
                    </Badge>
                  </td>
                  <td className="py-2.5 text-muted-foreground">{fmtTime(v.created_at)}</td>
                  <td className="py-2.5 text-muted-foreground">{fmtTime(v.last_used_at)}</td>
                  <td className="py-2.5 text-right">
                    <div className="inline-flex gap-1">
                      <Button size="sm" variant="ghost" onClick={() => toggle(v)}>
                        {v.enabled ? '禁用' : '启用'}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-destructive"
                        onClick={() => remove(v)}
                      >
                        删除
                      </Button>
                    </div>
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

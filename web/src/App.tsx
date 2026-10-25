import { useEffect, useState } from 'react'
import { ApiError, clearToken, getToken, setToken } from './api'
import { Button, Card, CardContent, CardHeader, CardTitle, Field, Input } from './components/ui'
import Dashboard from './pages/Dashboard'
import Providers from './pages/Providers'
import Models from './pages/Models'
import VKeys from './pages/VKeys'
import { cn } from './lib/utils'

type Tab = 'dashboard' | 'providers' | 'models' | 'vkeys'

const TABS: Array<{ id: Tab; label: string }> = [
  { id: 'dashboard', label: '用量总览' },
  { id: 'providers', label: 'Providers' },
  { id: 'models', label: '模型登记' },
  { id: 'vkeys', label: '虚拟 Key' },
]

export default function App() {
  const [dark, setDark] = useState(() => localStorage.getItem('veyra_theme') !== 'light')
  const [token, setTok] = useState(getToken())
  const [authed, setAuthed] = useState(false)
  const [authErr, setAuthErr] = useState('')
  const [tab, setTab] = useState<Tab>('dashboard')
  const [fatal, setFatal] = useState('')

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem('veyra_theme', dark ? 'dark' : 'light')
  }, [dark])

  // 已存令牌的启动校验：无效则回到输入页。
  useEffect(() => {
    if (!token) return
    fetch('/admin/api/stats', { headers: { Authorization: `Bearer ${token}` } }).then((r) => {
      if (r.ok) setAuthed(true)
      else {
        clearToken()
        setTok('')
        setAuthErr('已保存的令牌无效，请重新输入')
      }
    })
  }, [token])

  async function submitToken() {
    const res = await fetch('/admin/api/stats', {
      headers: { Authorization: `Bearer ${token}` },
    })
    if (res.ok) {
      setToken(token)
      setAuthed(true)
      setAuthErr('')
    } else {
      setAuthErr('令牌无效（VEYRA_ADMIN_TOKEN）')
    }
  }

  function onApiError(e: unknown) {
    if (e instanceof ApiError && e.status === 401) {
      clearToken()
      setTok('')
      setAuthed(false)
      setAuthErr('令牌已失效，请重新输入')
    } else {
      setFatal(e instanceof Error ? e.message : String(e))
    }
  }

  if (!authed) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-background p-4">
        <Card className="w-full max-w-sm">
          <CardHeader>
            <div className="mb-2 text-lg font-semibold tracking-tight">
              Veyra <span className="text-muted-foreground font-normal">控制台</span>
            </div>
            <CardTitle>输入管理令牌</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <Field label="VEYRA_ADMIN_TOKEN" hint="令牌只经请求头传输，不落 URL。">
              <Input
                type="password"
                value={token}
                autoFocus
                onChange={(e) => setTok(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && submitToken()}
                placeholder="admin token"
              />
            </Field>
            {authErr && <p className="text-xs text-destructive">{authErr}</p>}
            <Button onClick={submitToken}>进入</Button>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="flex min-h-screen bg-background">
      <aside className="flex w-52 shrink-0 flex-col border-r bg-card">
        <div className="flex items-center gap-2 px-5 py-5">
          <div className="flex h-7 w-7 items-center justify-center rounded-md bg-primary text-xs font-bold text-primary-foreground">
            V
          </div>
          <span className="text-base font-semibold tracking-tight">Veyra</span>
        </div>
        <nav className="flex flex-col gap-1 px-3">
          {TABS.map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={cn(
                'rounded-md px-3 py-2 text-left text-sm transition-colors',
                tab === t.id
                  ? 'bg-primary/15 font-medium text-primary'
                  : 'text-muted-foreground hover:bg-secondary hover:text-foreground',
              )}
            >
              {t.label}
            </button>
          ))}
        </nav>
        <div className="mt-auto flex items-center justify-between px-5 py-4">
          <button
            onClick={() => setDark(!dark)}
            className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-secondary hover:text-foreground"
          >
            {dark ? '浅色' : '深色'}
          </button>
          <button
            onClick={() => {
              clearToken()
              setTok('')
              setAuthed(false)
            }}
            className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-secondary hover:text-foreground"
          >
            退出
          </button>
        </div>
      </aside>
      <main className="min-w-0 flex-1 p-6 lg:p-8">
        {fatal && (
          <div className="mb-4 flex items-center justify-between rounded-md border border-destructive/40 bg-destructive/10 px-4 py-2 text-sm text-destructive">
            <span>{fatal}</span>
            <button className="text-xs underline" onClick={() => setFatal('')}>
              关闭
            </button>
          </div>
        )}
        {tab === 'dashboard' && <Dashboard onError={onApiError} />}
        {tab === 'providers' && <Providers onError={onApiError} />}
        {tab === 'models' && <Models onError={onApiError} />}
        {tab === 'vkeys' && <VKeys onError={onApiError} />}
      </main>
    </div>
  )
}

import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { AccountSummary, PoolConfig } from '../api/types'
import AsyncState from '../components/AsyncState'
import AccountFormDialog from '../components/AccountFormDialog'
import CookieDialog from '../components/CookieDialog'
import ICloudLoginDialog from '../components/ICloudLoginDialog'
import AppPasswordDialog from '../components/AppPasswordDialog'
import ProxyDialog from '../components/ProxyDialog'
import MailboxDialog from '../components/MailboxDialog'
import PoolDialog from '../components/PoolDialog'
import ConfirmDialog from '../components/ConfirmDialog'
import DropdownMenu from '../components/DropdownMenu'
import PageHeader from '../components/PageHeader'
import Pagination from '../components/Pagination'
import { useToast } from '../components/ToastProvider'
import {
  IconCheck,
  IconClock,
  IconAlert,
  IconPlus,
  IconEdit,
  IconTrash,
  IconKey,
  IconMail,
  IconRefresh,
  IconSearch,
} from '../components/icons'

const statusMeta: Record<string, { text: string; badge: string; icon: typeof IconCheck }> = {
  active: { text: '正常', badge: 'badge badge-active', icon: IconCheck },
  // 添加账号后由服务端后台校验,前端轮询到这个状态就静默刷新
  validating: { text: '校验中', badge: 'badge badge-pending', icon: IconClock },
  pending: { text: '待配置', badge: 'badge badge-pending', icon: IconClock },
  error: { text: '异常', badge: 'badge badge-error', icon: IconAlert },
}

function StatusBadge({ status }: { status: string }) {
  const meta = statusMeta[status] ?? {
    text: status,
    badge: 'badge badge-neutral',
    icon: IconClock,
  }
  const Icon = meta.icon
  return (
    <span className={meta.badge}>
      <Icon />
      {meta.text}
    </span>
  )
}

function credText(acc: AccountSummary): string {
  const parts: string[] = []
  if (acc.has_cookies) parts.push('Cookie')
  if (acc.has_app_password) parts.push('App密码')
  if (acc.mailbox) parts.push(`收件箱:${acc.mailbox.email}`)
  if (acc.has_proxy) parts.push('代理')
  return parts.length > 0 ? `已配置（${parts.join('·')}）` : '未配置'
}

/** 把 ISO 时间格式化为紧凑的本地时间,避免表格里出现长串。 */
function formatValidated(raw: string): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export default function AccountsPage() {
  const [accounts, setAccounts] = useState<AccountSummary[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  // 客户端分页
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [search, setSearch] = useState('')

  // dialog 状态
  const [formOpen, setFormOpen] = useState(false)
  const [editing, setEditing] = useState<AccountSummary | null>(null)
  const [cookieFor, setCookieFor] = useState<AccountSummary | null>(null)
  const [loginFor, setLoginFor] = useState<AccountSummary | null>(null)
  const [appPwdFor, setAppPwdFor] = useState<AccountSummary | null>(null)
  const [proxyFor, setProxyFor] = useState<AccountSummary | null>(null)
  const [mailboxFor, setMailboxFor] = useState<AccountSummary | null>(null)
  const [deleteFor, setDeleteFor] = useState<AccountSummary | null>(null)
  const [deleting, setDeleting] = useState(false)
  // 自动补号配置:key 为账号 ID。接口未启用时保持空对象,页面不显示相关入口。
  const [poolConfigs, setPoolConfigs] = useState<Record<string, PoolConfig>>({})
  const [poolFor, setPoolFor] = useState<AccountSummary | null>(null)
  const [poolAvailable, setPoolAvailable] = useState(true)
  // 保存补号配置后 +1,触发上面 effect 重新拉取
  const [poolRefreshKey, setPoolRefreshKey] = useState(0)
  // 后台校验的轮询次数上限:60s 仍未有结果就停止,避免异常时无限打服务端
  const pollAttempts = useRef(0)

  const { show } = useToast()

  // 当前页数据(先按搜索词过滤,再分页)
  const filteredAccounts = accounts.filter((acc) => {
    const q = search.trim().toLowerCase()
    if (!q) return true
    return (
      acc.name.toLowerCase().includes(q) ||
      (acc.icloud_email ?? '').toLowerCase().includes(q) ||
      (acc.real_email ?? '').toLowerCase().includes(q)
    )
  })
  const totalPages = Math.max(1, Math.ceil(filteredAccounts.length / pageSize))
  const safePage = Math.min(page, totalPages)
  const pageAccounts = filteredAccounts.slice((safePage - 1) * pageSize, safePage * pageSize)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await request<AccountSummary[]>('/api/accounts')
      setAccounts(data)
      setError('')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    let cancelled = false
    request<AccountSummary[]>('/api/accounts')
      .then((data) => {
        if (cancelled) return
        setAccounts(data)
        setError('')
      })
      .catch((err) => {
        if (cancelled) return
        setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [retryKey])

  // 添加账号后服务端在后台校验会话,这里有 validating 就静默轮询等结果。
  // 用 accounts 自身作为依赖:每次刷新拿到新数组会重新排一次定时器,
  // 全部离开 validating 后轮询自然停止。
  useEffect(() => {
    if (!accounts.some((acc) => acc.status === 'validating')) {
      pollAttempts.current = 0
      return
    }
    if (pollAttempts.current >= 30) return
    const timer = window.setTimeout(() => {
      pollAttempts.current += 1
      request<AccountSummary[]>('/api/accounts')
        .then((data) => {
          setAccounts(data)
          setError('')
        })
        .catch(() => {
          // 静默失败:不打扰用户,下一轮再试(达到次数上限自动停)
          setAccounts((prev) => [...prev])
        })
    }, 2000)
    return () => window.clearTimeout(timer)
  }, [accounts])

  // 自动补号配置单独拉取:该功能可能未启用(503),失败时静默降级为不显示
  useEffect(() => {
    let cancelled = false
    request<{ configs: PoolConfig[] }>('/api/pool')
      .then((data) => {
        if (cancelled) return
        const map: Record<string, PoolConfig> = {}
        for (const cfg of data.configs ?? []) map[cfg.account_id] = cfg
        setPoolConfigs(map)
        setPoolAvailable(true)
      })
      .catch(() => {
        if (cancelled) return
        setPoolConfigs({})
        setPoolAvailable(false)
      })
    return () => {
      cancelled = true
    }
  }, [retryKey, poolRefreshKey])

  function handleRetry() {
    setLoading(true)
    setRetryKey((k) => k + 1)
  }

  async function handleDelete() {
    if (!deleteFor) return
    setDeleting(true)
    try {
      await request(`/api/accounts/${deleteFor.id}`, { method: 'DELETE' })
      setDeleteFor(null)
      show('账号已删除')
      void load()
    } catch (err) {
      show(err instanceof ApiError ? err.message : '删除失败')
    } finally {
      setDeleting(false)
    }
  }

  return (
    <section>
      <PageHeader
        title="账号管理"
        actions={
          <button
            className="primary"
            onClick={() => {
              setEditing(null)
              setFormOpen(true)
            }}
          >
            <IconPlus size={16} />
            添加账号
          </button>
        }
      />

      <div className="data-panel">
      <div className="toolbar">
        <div className="toolbar-field toolbar-grow">
          <label htmlFor="account-search">搜索</label>
          <div className="search-input">
            <input
              id="account-search"
              type="search"
              value={search}
              onChange={(e) => {
                setSearch(e.target.value)
                setPage(1)
              }}
              placeholder="按名称或邮箱搜索"
            />
            <IconSearch size={16} />
          </div>
        </div>
      </div>
      <AsyncState
        loading={loading}
        error={error}
        empty={filteredAccounts.length === 0}
        emptyText={accounts.length === 0 ? '暂无账号，点击“添加账号”开始' : '没有匹配的账号'}
        onRetry={handleRetry}
      >
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>名称</th>
                <th>邮箱</th>
                <th>状态</th>
                <th>别名</th>
                <th>凭据</th>
                <th>最近验证</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {pageAccounts.map((acc) => (
                <tr key={acc.id}>
                  <td>
                    {acc.name}
                    {acc.status_message && (
                      <span className="hint" style={{ display: 'block' }}>
                        {acc.status_message}
                      </span>
                    )}
                  </td>
                  <td>
                    {acc.icloud_email || acc.real_email || '—'}
                    <span className="cell-secondary">{acc.id}</span>
                  </td>
                  <td>
                    <StatusBadge status={acc.status} />
                  </td>
                  <td>
                    <span className="cell-strong">
                      {acc.alias_active} / {acc.alias_total}
                    </span>
                    {poolConfigs[acc.id] && (
                      <span className="cell-secondary">
                        {poolConfigs[acc.id].enabled
                          ? `自动补至 ${poolConfigs[acc.id].target} · 已建 ${poolConfigs[acc.id].total_created}`
                          : '自动补号已关闭'}
                        {poolConfigs[acc.id].paused ? ' · 已暂停' : ''}
                      </span>
                    )}
                  </td>
                  <td>{credText(acc)}</td>
                  <td>{formatValidated(acc.last_validated)}</td>
                  <td>
                    <div className="row-actions">
                      <button onClick={() => setCookieFor(acc)} title="更新 iCloud Cookie">
                        更新 Cookie
                      </button>
                      <button onClick={() => setAppPwdFor(acc)} title="设置 App 专用密码">
                        App 密码
                      </button>
                      <Link to={`/aliases?account_id=${acc.id}`}>别名</Link>
                      <DropdownMenu label={`更多操作（${acc.name}）`} trigger="更多">
                        <button onClick={() => { setEditing(acc); setFormOpen(true) }}>
                          <IconEdit size={14} />
                          编辑
                        </button>
                        <button onClick={() => setLoginFor(acc)}>
                          <IconKey size={14} />
                          iCloud 登录
                        </button>
                        <button onClick={() => setMailboxFor(acc)}>接入收件邮箱</button>
                        <button onClick={() => setProxyFor(acc)}>设置代理</button>
                        {poolAvailable && (
                          <button onClick={() => setPoolFor(acc)}>
                            <IconRefresh size={14} />
                            自动补号
                          </button>
                        )}
                        <Link to={`/inbox?account_id=${acc.id}`}>
                          <IconMail size={14} />
                          收件箱
                        </Link>
                        <button className="danger" onClick={() => setDeleteFor(acc)}>
                          <IconTrash size={14} />
                          删除
                        </button>
                      </DropdownMenu>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </AsyncState>
        {!error && (
          <Pagination
            page={safePage}
            pageSize={pageSize}
            total={filteredAccounts.length}
            onPageChange={setPage}
            onPageSizeChange={(size) => {
              setPageSize(size)
              setPage(1)
            }}
            showWhenEmpty
          />
        )}
      </div>

      <AccountFormDialog
        open={formOpen}
        onClose={() => setFormOpen(false)}
        onSaved={(info) => {
          setFormOpen(false)
          show(info.validating ? '账号已添加，正在后台校验凭据' : '账号已保存')
          void load()
        }}
        editing={editing ? { id: editing.id, name: editing.name, icloudEmail: editing.icloud_email, host: editing.host } : null}
      />
      {cookieFor && (
        <CookieDialog
          accountId={cookieFor.id}
          open
          onClose={() => setCookieFor(null)}
          onSaved={() => {
            show('Cookie 已保存，正在后台校验')
            void load()
          }}
        />
      )}
      {loginFor && (
        <ICloudLoginDialog
          accountId={loginFor.id}
          open
          onClose={() => setLoginFor(null)}
          onSaved={() => {
            show('登录成功')
            void load()
          }}
        />
      )}
      {appPwdFor && (
        <AppPasswordDialog
          accountId={appPwdFor.id}
          open
          onClose={() => setAppPwdFor(null)}
          onSaved={() => {
            show('App 专用密码已设置')
            void load()
          }}
        />
      )}
      {proxyFor && (
        <ProxyDialog
          accountId={proxyFor.id}
          open
          onClose={() => setProxyFor(null)}
          onSaved={() => {
            show('代理已更新')
            void load()
          }}
        />
      )}
      {mailboxFor && (
        <MailboxDialog
          key={mailboxFor.id}
          accountId={mailboxFor.id}
          current={mailboxFor.mailbox}
          open
          onClose={() => setMailboxFor(null)}
          onSaved={() => { setMailboxFor(null); show('收件邮箱已接入'); void load() }}
        />
      )}
      {poolFor && (
        <PoolDialog
          key={poolFor.id}
          accountId={poolFor.id}
          accountName={poolFor.name}
          config={poolConfigs[poolFor.id] ?? null}
          open
          onClose={() => setPoolFor(null)}
          onSaved={(message) => {
            setPoolFor(null)
            show(message)
            setPoolRefreshKey((k) => k + 1)
            void load()
          }}
        />
      )}
      {deleteFor && (
        <ConfirmDialog
          title="删除账号"
          message={`将移除本地账号配置「${deleteFor.name}」，不会删除 Apple 账号本身。`}
          requireText={deleteFor.name}
          open
          busy={deleting}
          onClose={() => setDeleteFor(null)}
          onConfirm={() => void handleDelete()}
        />
      )}
    </section>
  )
}

import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { AccountSummary, Alias } from '../api/types'
import AsyncState from '../components/AsyncState'
import PageHeader from '../components/PageHeader'
import {
  IconAccounts,
  IconAliases,
  IconCheck,
  IconClock,
  IconInbox,
  IconSend,
} from '../components/icons'

/** 单个账号的统计快照。 */
interface AccountStats {
  account: AccountSummary
  aliases: Alias[]
  error: string
}

/** 汇总统计卡片的单项。 */
function StatCard({
  label,
  value,
  hint,
  icon,
  tone,
}: {
  label: string
  value: number | string
  hint?: string
  icon: React.ReactNode
  tone?: 'primary' | 'success' | 'muted'
}) {
  return (
    <div className={`stat-card${tone ? ` tone-${tone}` : ''}`}>
      <div className="stat-icon" aria-hidden="true">
        {icon}
      </div>
      <div className="stat-body">
        <span className="stat-label">{label}</span>
        <strong className="stat-value">{value}</strong>
        {hint && <span className="stat-hint">{hint}</span>}
      </div>
    </div>
  )
}

export default function DashboardPage() {
  const [stats, setStats] = useState<AccountStats[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const [searchParams] = useSearchParams()

  useEffect(() => {
    let cancelled = false
    request<AccountSummary[]>('/api/accounts')
      .then(async (accounts) => {
        if (cancelled) return
        // 并发拉取每个账号的别名列表,单个账号失败不影响整体
        const results = await Promise.all(
          accounts.map(async (account) => {
            try {
              const data = await request<{ aliases: Alias[] }>(
                `/api/aliases?account_id=${encodeURIComponent(account.id)}`,
              )
              return { account, aliases: data.aliases ?? [], error: '' }
            } catch (err) {
              return {
                account,
                aliases: [] as Alias[],
                error: err instanceof ApiError ? err.message : '读取别名失败',
              }
            }
          }),
        )
        if (cancelled) return
        setStats(results)
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

  const allAliases = stats.flatMap((s) => s.aliases)
  const activeCount = allAliases.filter((a) => a.active).length
  const inactiveCount = allAliases.length - activeCount
  const accountCount = stats.length
  const failedAccounts = stats.filter((s) => s.error).length

  // 最近创建的别名(按创建时间倒序,取前 8 条)
  const recent = [...allAliases]
    .filter((a) => a.createdAt)
    .sort((a, b) => String(b.createdAt).localeCompare(String(a.createdAt)))
    .slice(0, 8)

  const empty = !loading && !error && accountCount === 0

  return (
    <section>
      <PageHeader
        title="分发总览"
        actions={
          <>
            <Link className="btn-link" to={`/aliases?account_id=${encodeURIComponent(searchParams.get('account_id') ?? '')}`}>
              <IconAliases size={14} />
              管理隐藏邮箱
            </Link>
            <Link className="btn-link" to="/inbox">
              <IconInbox size={14} />
              查看收件箱
            </Link>
          </>
        }
      />

      {empty ? (
        <p className="empty-state">
          还没有添加账号。先到「账号」页面添加一个 iCloud 账号，再创建隐藏邮箱分发给他人。
        </p>
      ) : (
        <AsyncState
          loading={loading}
          error={error}
          empty={false}
          onRetry={() => {
            setLoading(true)
            setRetryKey((k) => k + 1)
          }}
        >
          <div className="stat-grid">
            <StatCard
              label="隐藏邮箱总数"
              value={allAliases.length}
              hint={`分布在 ${accountCount} 个账号`}
              icon={<IconAliases size={16} />}
              tone="primary"
            />
            <StatCard
              label="可接收邮件"
              value={activeCount}
              hint="状态为已启用"
              icon={<IconCheck size={16} />}
              tone="success"
            />
            <StatCard
              label="已停用"
              value={inactiveCount}
              hint="不再接收邮件"
              icon={<IconClock size={16} />}
              tone="muted"
            />
            <StatCard
              label="账号"
              value={accountCount}
              hint={failedAccounts > 0 ? `${failedAccounts} 个账号读取失败` : '全部正常'}
              icon={<IconAccounts size={16} />}
            />
          </div>

          <div className="panel-grid">
            <section className="panel">
              <header className="panel-head">
                <h3>账号状态</h3>
              </header>
              <div className="table-wrap flat">
                <table>
                  <thead>
                    <tr>
                      <th>账号</th>
                      <th>隐藏邮箱</th>
                      <th>状态</th>
                    </tr>
                  </thead>
                  <tbody>
                    {stats.map(({ account, aliases, error: aliasError }) => (
                      <tr key={account.id}>
                        <td>
                          <div className="cell-stack">
                            <Link className="link-strong" to={`/aliases?account_id=${encodeURIComponent(account.id)}`}>
                              {account.name}
                            </Link>
                            <span className="cell-secondary">{account.icloud_email || account.real_email || '—'}</span>
                          </div>
                        </td>
                        <td>
                          <span className="num">{aliases.length}</span>
                        </td>
                        <td>
                          {aliasError ? (
                            <span className="badge badge-error" title={aliasError}>读取失败</span>
                          ) : (
                            <span className={
                              account.has_app_password ? 'badge badge-active' : 'badge badge-pending'
                            }>
                              {account.has_app_password ? 'IMAP 就绪' : '仅 Cookie'}
                            </span>
                          )}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </section>

            <section className="panel">
              <header className="panel-head">
                <h3>最近创建的隐藏邮箱</h3>
              </header>
              {recent.length === 0 ? (
                <p className="panel-empty">暂无隐藏邮箱</p>
              ) : (
                <ul className="mini-list">
                  {recent.map((alias) => (
                    <li key={alias.anonymousId || alias.email}>
                      <div className="mini-list-main">
                        <span className="mono">{alias.email}</span>
                        <span className="cell-secondary">{alias.label || '未命名'}</span>
                      </div>
                      <span className={alias.active ? 'badge badge-active' : 'badge badge-neutral'}>
                        {alias.active ? '启用' : '停用'}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </div>

          <div className="callout">
            <IconSend size={16} />
            <div>
              <strong>分发与转发</strong>
              <p>
                把隐藏邮箱分发给他人后，可用「待转发」视图按别名查看未转发的邮件，
                再通过接口 <code>/api/forward</code> 拉取并登记已转发，避免重复转发。
              </p>
            </div>
          </div>
        </AsyncState>
      )}
    </section>
  )
}

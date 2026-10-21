import { useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { AccountSummary, Alias, ForwardResult, FullMessage, InboxResult, InboxMessage } from '../api/types'
import AsyncState from '../components/AsyncState'
import Dialog from '../components/Dialog'
import ConfirmDialog from '../components/ConfirmDialog'
import PageHeader from '../components/PageHeader'
import Pagination from '../components/Pagination'
import { useToast } from '../components/ToastProvider'
import { IconKey, IconRefresh, IconTrash } from '../components/icons'

/** 「全部账号」在筛选器中的保留值(不是真实账号 id)。 */
const ALL_ACCOUNTS = '__all__'

function formatDate(raw: string): string {
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(d)
}

export default function InboxPage() {
  const [accounts, setAccounts] = useState<AccountSummary[]>([])
  const [aliases, setAliases] = useState<Alias[]>([])
  const [accountId, setAccountId] = useState('')
  const [alias, setAlias] = useState('')
  const [limit, setLimit] = useState(20)
  const [days, setDays] = useState(7)
  // 客户端分页页码(从 1 开始)
  const [page, setPage] = useState(1)

  const [result, setResult] = useState<InboxResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const [detail, setDetail] = useState<FullMessage | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [deleteFor, setDeleteFor] = useState<InboxMessage | null>(null)
  const [deleting, setDeleting] = useState(false)

  const [searchParams, setSearchParams] = useSearchParams()
  const abortRef = useRef<AbortController | null>(null)
  const { show } = useToast()

  // 待转发视图: 只显示尚未转发的新邮件(转发服务的"待办"视角)
  const [forwardView, setForwardView] = useState(false)
  const [forward, setForward] = useState<ForwardResult | null>(null)
  const [forwardLoading, setForwardLoading] = useState(false)
  const [forwardError, setForwardError] = useState('')
  const [acking, setAcking] = useState(false)

  // 仅在选择了具体别名时,才能按别名查待转发的邮件
  const forwardActive = forwardView && Boolean(accountId) && Boolean(alias)
  useEffect(() => {
    if (!forwardActive) return
    let cancelled = false
    const controller = new AbortController()
    const params = new URLSearchParams({
      account_id: accountId,
      alias,
      limit: String(limit),
      days: String(days),
    })
    request<ForwardResult>(`/api/forward?${params.toString()}`, { signal: controller.signal })
      .then((data) => {
        if (cancelled) return
        setForward(data)
        setForwardError('')
      })
      .catch((err) => {
        if (cancelled || (err instanceof ApiError && err.status === 0)) return
        setForwardError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
        setForward(null)
      })
      .finally(() => {
        if (!cancelled) setForwardLoading(false)
      })
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [forwardActive, accountId, alias, limit, days, retryKey])

  async function markAllForwarded() {
    if (!forward || forward.messages.length === 0) return
    setAcking(true)
    try {
      await request('/api/forward/ack', {
        method: 'POST',
        body: JSON.stringify({
          account_id: accountId,
          alias,
          ids: forward.messages.map((m) => m.id),
        }),
      })
      show(`已标记 ${forward.messages.length} 封为已转发`)
      setRetryKey((k) => k + 1)
    } catch (err) {
      show(err instanceof ApiError ? err.message : '标记失败')
    } finally {
      setAcking(false)
    }
  }

  async function openMessage(message: InboxMessage) {
    setDetailLoading(true)
    setDetail(null)
    try {
      const data = await request<FullMessage>(`/api/inbox/${encodeURIComponent(message.id)}?account_id=${encodeURIComponent(accountId)}`)
      setDetail(data)
    } catch (err) {
      show(err instanceof ApiError ? err.message : '读取邮件详情失败')
    } finally {
      setDetailLoading(false)
    }
  }

  async function deleteMessage() {
    if (!deleteFor) return
    setDeleting(true)
    try {
      await request(`/api/inbox/${encodeURIComponent(deleteFor.id)}?account_id=${encodeURIComponent(accountId)}`, { method: 'DELETE' })
      setDeleteFor(null)
      setDetail(null)
      show('邮件已删除')
      setRetryKey((key) => key + 1)
    } catch (err) {
      show(err instanceof ApiError ? err.message : '删除邮件失败')
    } finally {
      setDeleting(false)
    }
  }

  // 加载账号列表并初始化筛选状态(只保存 account_id/alias/limit/days)
  useEffect(() => {
    let cancelled = false
    request<AccountSummary[]>('/api/accounts')
      .then((data) => {
        if (cancelled) return
        setAccounts(data)
        const queryId = searchParams.get('account_id')
        // 默认展示「全部账号」;只有显式指定了有效账号 id 才切换到该账号
        const valid = data.find((a) => a.id === queryId)
        const target = valid ? valid.id : data.length > 0 ? ALL_ACCOUNTS : ''
        setAccountId(target)
        if (target) {
          const next: Record<string, string> = { account_id: target }
          const qAlias = searchParams.get('alias')
          if (qAlias) {
            setAlias(qAlias)
            next.alias = qAlias
          }
          const qLimit = searchParams.get('limit')
          if (qLimit) next.limit = qLimit
          const qDays = searchParams.get('days')
          if (qDays) next.days = qDays
          setSearchParams(next, { replace: true })
        }
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 账号变化时加载别名列表(供筛选);「全部账号」时合并所有账号的别名
  useEffect(() => {
    if (!accountId) return
    let cancelled = false
    const ids = accountId === ALL_ACCOUNTS ? accounts.map((a) => a.id) : [accountId]
    Promise.all(
      ids.map((id) =>
        request<{ aliases: Alias[] }>(`/api/aliases?account_id=${encodeURIComponent(id)}`)
          .then((d) => d.aliases ?? [])
          .catch(() => [] as Alias[]),
      ),
    )
      .then((lists) => {
        if (cancelled) return
        // 去重(同一别名理论上不会跨账号重复,防御性处理)
        const seen = new Set<string>()
        const merged: Alias[] = []
        for (const a of lists.flat()) {
          if (seen.has(a.email)) continue
          seen.add(a.email)
          merged.push(a)
        }
        setAliases(merged)
      })
      .catch(() => {
        if (cancelled) return
        setAliases([])
      })
    return () => {
      cancelled = true
    }
  }, [accountId, accounts])

  // 查询收件箱;账号变化时清空旧邮件并中止旧请求。
  // 「全部账号」模式下并发拉取各账号并合并,按时间倒序。
  useEffect(() => {
    if (!accountId) return
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    let cancelled = false

    const ids = accountId === ALL_ACCOUNTS ? accounts.map((a) => a.id) : [accountId]

    function fetchOne(id: string) {
      const params = new URLSearchParams({ account_id: id })
      if (alias) params.set('alias', alias)
      params.set('limit', String(limit))
      params.set('days', String(days))
      return request<InboxResult>(`/api/inbox?${params.toString()}`, { signal: controller.signal })
    }

    // 注意:不在这里同步 setLoading,避免级联渲染;loading 由触发查询的事件置位
    Promise.all(
      ids.map((id) =>
        fetchOne(id)
          .then((r) => ({ ok: true as const, result: r }))
          .catch((e) => ({ ok: false as const, error: e })),
      ),
    )
      .then((results) => {
        if (cancelled) return
        const okOnes = results.filter((r) => r.ok)
        if (okOnes.length === 0) {
          const firstErr = results[0]
          const err = firstErr && !firstErr.ok ? firstErr.error : null
          if (err instanceof ApiError && err.status === 0) return
          setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
          setResult(null)
          return
        }
        // 合并并按日期倒序(无日期的排最后)
        const messages = okOnes.flatMap((r) => r.result.messages)
        messages.sort((a, b) => {
          const ta = Date.parse(a.date)
          const tb = Date.parse(b.date)
          if (Number.isNaN(ta)) return 1
          if (Number.isNaN(tb)) return -1
          return tb - ta
        })
        setResult({
          account_id: accountId,
          alias: alias || undefined,
          count: messages.length,
          method: okOnes[0].result.method,
          messages,
        })
        setError('')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })

    return () => {
      cancelled = true
      controller.abort()
    }
  }, [accountId, accounts, alias, limit, days, retryKey])

  function handleSearch() {
    const next: Record<string, string> = { account_id: accountId }
    if (alias) next.alias = alias
    next.limit = String(limit)
    next.days = String(days)
    setSearchParams(next, { replace: true })
    setPage(1)
    setRetryKey((k) => k + 1)
  }

  function handleAccountChange(id: string) {
    setAccountId(id)
    setAlias('')
    setPage(1)
    setResult(null)
    setLoading(true)
    setSearchParams({ account_id: id }, { replace: true })
  }

  const methodText = result?.method === 'imap' ? 'IMAP' : 'Web API'

  // 客户端分页:limit 既作为拉取上限,也作为每页条数
  const allMessages = result?.messages ?? []
  const safePage = Math.min(page, Math.max(1, Math.ceil(allMessages.length / limit)))
  const pageMessages = allMessages.slice((safePage - 1) * limit, safePage * limit)

  return (
    <section>
      <PageHeader
        title="收件箱"
      />

      <div className="data-panel">
      <div className="toolbar">
        <div className="toolbar-field">
          <label htmlFor="inbox-account">账号</label>
          <select
            id="inbox-account"
            value={accountId}
            onChange={(e) => handleAccountChange(e.target.value)}
            style={{ width: 'auto', minWidth: 140 }}
          >
            <option value={ALL_ACCOUNTS}>全部账号</option>
            {accounts.map((a) => (
              <option key={a.id} value={a.id}>
                {a.name}
              </option>
            ))}
          </select>
        </div>
        <div className="toolbar-field">
          <label htmlFor="inbox-alias">别名</label>
          <select
            id="inbox-alias"
            value={alias}
            onChange={(e) => {
              setAlias(e.target.value)
              setPage(1)
              setLoading(true)
            }}
            style={{ width: 'auto', minWidth: 180 }}
          >
            <option value="">全部</option>
            {aliases.map((a) => (
              <option key={a.anonymousId} value={a.email}>
                {a.email}
              </option>
            ))}
          </select>
        </div>
        <div className="toolbar-field">
          <label htmlFor="inbox-days">时间范围</label>
          <select
            id="inbox-days"
            value={days}
            onChange={(e) => {
              setDays(Number(e.target.value))
              setPage(1)
              setLoading(true)
            }}
            style={{ width: 'auto' }}
          >
            <option value={1}>1 天</option>
            <option value={7}>7 天</option>
            <option value={30}>30 天</option>
            <option value={90}>90 天</option>
          </select>
        </div>
        <div className="toolbar-actions">
          <button className="primary" onClick={handleSearch} disabled={!accountId}>
            查询
          </button>
          <button
            type="button"
            onClick={() => {
              // 在事件里置 loading,避免在 effect 内同步 setState
              setForwardView((v) => !v)
              setForwardLoading(true)
              setForwardError('')
            }}
            disabled={!alias || accountId === ALL_ACCOUNTS}
            title={
              accountId === ALL_ACCOUNTS
                ? '「待转发」需要先选择一个具体账号'
                : alias
                  ? '只显示尚未转发的邮件'
                  : '请先选择具体别名'
            }
            aria-pressed={forwardView}
          >
            <IconRefresh size={14} />
            待转发
          </button>
        </div>
      </div>
      {!alias && forwardView && accountId !== ALL_ACCOUNTS && (
        <p className="hint hint-block">「待转发」需要先选择具体别名。</p>
      )}

      {forwardActive && (
        <>
          <p className="hint" style={{ marginBottom: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
            <span>待转发 {forward?.count ?? 0} 封</span>
            <span className="badge badge-neutral">别名：{alias}</span>
            <button
              type="button"
              disabled={acking || !forward || forward.messages.length === 0}
              onClick={() => void markAllForwarded()}
            >
              {acking ? '标记中…' : '全部标记为已转发'}
            </button>
          </p>
          <AsyncState
            loading={forwardLoading}
            error={forwardError}
            empty={!forward || forward.messages.length === 0}
            emptyText="没有待转发的邮件"
            onRetry={() => setRetryKey((k) => k + 1)}
          >
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>主题</th>
                    <th>发件人</th>
                    <th>日期</th>
                    <th>摘要</th>
                  </tr>
                </thead>
                <tbody>
                  {forward?.messages.map((m) => (
                    <tr key={m.id}>
                      <td><button className="link-button" onClick={() => void openMessage(m)}>{m.subject || '（无主题）'}</button></td>
                      <td>{m.from}</td>
                      <td>{formatDate(m.date)}</td>
                      <td>{m.preview || '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </AsyncState>
        </>
      )}

      {accounts.length === 0 && !loading && !error && (
        <p className="empty-state">暂无账号，请先到「账号」页面添加账号</p>
      )}
      {accounts.length > 0 && !forwardActive && (

      <AsyncState
        loading={loading}
        error={error}
        empty={!result || result.messages.length === 0}
        emptyText="暂无邮件"
        onRetry={() => {
          setLoading(true)
          setRetryKey((k) => k + 1)
        }}
      >
        {result && result.messages.length > 0 && (
          <>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>主题</th>
                    <th>发件人</th>
                    <th>收件人</th>
                    <th>日期</th>
                    <th>摘要</th>
                  </tr>
                </thead>
                <tbody>
                  {pageMessages.map((m) => (
                    <tr key={m.id}>
                      <td><button className="link-button" onClick={() => void openMessage(m)}>{m.subject || '（无主题）'}</button></td>
                      <td>{m.from}</td>
                      <td>{m.to}</td>
                      <td>{formatDate(m.date)}</td>
                      <td>
                        <div className="row-inline">
                          <span>{m.preview || '—'}</span>
                          <button className="icon-button danger" aria-label="删除邮件" title="删除邮件" onClick={() => setDeleteFor(m)}><IconTrash size={14} /></button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </AsyncState>
      )}
      {/* 出错时不显示分页(此时没有真实的分页语义),成功时即使 0 条也保留每页条数 */}
      {!error && (
        <Pagination
          page={safePage}
          pageSize={limit}
          total={result?.messages.length ?? 0}
          onPageChange={setPage}
          onPageSizeChange={(size) => {
            setLimit(size)
            setPage(1)
            setLoading(true)
          }}
          showWhenEmpty
          extra={result ? <span className="pagination-method"><IconKey size={12} />{methodText}</span> : undefined}
        />
      )}
      </div>
      <Dialog title={detail?.subject || '邮件详情'} open={detail !== null || detailLoading} onClose={() => { setDetail(null); setDetailLoading(false) }}>
        {detailLoading && <p className="hint">读取中…</p>}
        {!detailLoading && !detail && <p className="hint">未能读取邮件内容</p>}
        {detail && <>
          <p className="hint">发件人：{detail.from}</p>
          <p className="hint">收件人：{detail.to}</p>
          <p className="hint">日期：{formatDate(detail.date)}</p>
          <pre className="mail-body">{detail.body || '无正文'}</pre>
          <div className="form-actions"><button className="danger" onClick={() => setDeleteFor(detail)}>删除邮件</button><button onClick={() => setDetail(null)}>关闭</button></div>
        </>}
      </Dialog>
      {deleteFor && <ConfirmDialog title="删除邮件" message="邮件将从收件箱中永久删除。" open busy={deleting} onClose={() => setDeleteFor(null)} onConfirm={() => void deleteMessage()} />}
    </section>
  )
}

import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type {
  AccountSummary,
  Alias,
  AliasListResult,
  AliasMeta,
  BulkAliasAction,
  BulkAliasResult,
  BulkAliasTarget,
  Group,
} from '../api/types'
import AliasEditDialog from '../components/AliasEditDialog'
import AsyncState from '../components/AsyncState'
import CreateAliasDialog from '../components/CreateAliasDialog'
import ConfirmDialog from '../components/ConfirmDialog'
import PageHeader from '../components/PageHeader'
import Pagination from '../components/Pagination'
import { useToast } from '../components/ToastProvider'
import { copyText } from '../utils/clipboard'
import { downloadCSV, timestampedName } from '../utils/csv'
import {
  IconCheck,
  IconChevronDown,
  IconChevronUp,
  IconClock,
  IconDownload,
  IconEdit,
  IconInbox,
  IconPlus,
  IconSearch,
  IconTrash,
} from '../components/icons'

type SortDirection = 'asc' | 'desc'

/** 未分组的筛选值（分组 id 都以 g_ 开头，不会冲突） */
const NO_GROUP = '__none__'
const ALL_GROUPS = 'all'

/** 需要二次确认的批量操作；归组是本地改动，直接执行 */
type BulkConfirmAction = 'deactivate' | 'reactivate' | 'delete'

/** 批量操作的中文名，按钮和提示共用一套措辞 */
const BULK_LABEL: Record<BulkAliasAction, string> = {
  deactivate: '停用',
  reactivate: '激活',
  delete: '删除',
  assign_group: '归入分组',
}

function parseAliasDate(raw?: string): Date | null {
  const value = raw?.trim()
  if (!value) return null

  // iCloud 可能返回 ISO 字符串，也可能返回秒、毫秒或微秒时间戳。
  if (/^[+-]?\d+(?:\.\d+)?$/.test(value)) {
    const numeric = Number(value)
    if (Number.isFinite(numeric)) {
      const magnitude = Math.abs(numeric)
      const milliseconds =
        magnitude < 1e11
          ? numeric * 1000
          : magnitude < 1e14
            ? numeric
            : magnitude < 1e17
              ? numeric / 1000
              : numeric / 1e6
      const timestampDate = new Date(milliseconds)
      if (!Number.isNaN(timestampDate.getTime())) return timestampDate
    }
  }

  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date
}

function formatDate(raw?: string): string {
  const date = parseAliasDate(raw)
  if (!date) return raw?.trim() || '—'
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}/${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function dateTimestamp(raw?: string): number | null {
  return parseAliasDate(raw)?.getTime() ?? null
}

/**
 * 邮箱管理页。
 *
 * 展示的四个字段来自 iCloud：邮箱、标签、是否启用、创建时间。
 * 分组与备注是本系统额外加的（只存在本地），用于自己归类；
 * 邮箱地址与标签都由 iCloud 决定，本页不提供修改入口。
 */
export default function AliasesPage() {
  const [accounts, setAccounts] = useState<AccountSummary[]>([])
  const [accountId, setAccountId] = useState('')
  const [aliases, setAliases] = useState<Alias[]>([])
  const [meta, setMeta] = useState<Record<string, AliasMeta>>({})
  const [groups, setGroups] = useState<Group[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const [search, setSearch] = useState('')
  const [filter, setFilter] = useState<'all' | 'active' | 'inactive'>('all')
  const [sortDirection, setSortDirection] = useState<SortDirection>('desc')
  // 客户端分页:别名通常在几十到几百条,一次拉全量再本地分页体验最好
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<Alias | null>(null)
  // 单条操作带 alias；批量操作用 bulk 标记，区别于「哪一条」。
  const [confirm, setConfirm] = useState<
    | { type: BulkConfirmAction; alias: Alias }
    | { type: BulkConfirmAction; bulk: true }
    | null
  >(null)
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  // 勾选的别名（存 anonymousId，避免邮箱大小写/重复带来的歧义）
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  /** 批量归组的下拉值：'' 表示未选择，NO_GROUP 表示移出分组 */
  const [bulkGroup, setBulkGroup] = useState('')

  const [searchParams, setSearchParams] = useSearchParams()
  const groupFilter = searchParams.get('group_id') ?? ALL_GROUPS
  const { show, showCopyable } = useToast()

  function setGroupFilter(value: string) {
    const next = new URLSearchParams(searchParams)
    if (value === ALL_GROUPS) {
      next.delete('group_id')
    } else {
      next.set('group_id', value)
    }
    setSearchParams(next, { replace: true })
    setPage(1)
  }

  // 加载账号列表
  useEffect(() => {
    let cancelled = false
    request<AccountSummary[]>('/api/accounts')
      .then((data) => {
        if (cancelled) return
        setAccounts(data)
        const queryId = searchParams.get('account_id')
        const valid = data.find((a) => a.id === queryId)
        const target = valid ? valid.id : data[0]?.id ?? ''
        setAccountId(target)
        if (target && (!queryId || !valid)) {
          // 保留 group_id 等参数,从分组页跳进来时不能把筛选条件冲掉
          const next = new URLSearchParams(searchParams)
          next.set('account_id', target)
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

  // 加载分组(用于筛选与编辑)
  useEffect(() => {
    let cancelled = false
    request<{ groups: Group[] }>('/api/groups')
      .then((data) => {
        if (!cancelled) setGroups(data.groups ?? [])
      })
      .catch(() => {
        // 分组拉不到不影响邮箱列表本身,按"全部未分组"降级
      })
    return () => {
      cancelled = true
    }
  }, [retryKey])

  // 加载别名列表(响应里带本地分组/备注)
  useEffect(() => {
    if (!accountId) return
    let cancelled = false
    request<AliasListResult>(`/api/aliases?account_id=${encodeURIComponent(accountId)}`)
      .then((data) => {
        if (cancelled) return
        setAliases(data.aliases ?? [])
        setMeta(data.meta ?? {})
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
  }, [accountId, retryKey])

  const groupById = useMemo(() => {
    const map = new Map<string, Group>()
    for (const group of groups) map.set(group.id, group)
    return map
  }, [groups])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    return aliases
      .map((alias, index) => ({ alias, index }))
      .filter(({ alias }) => {
        if (filter === 'active' && !alias.active) return false
        if (filter === 'inactive' && alias.active) return false
        const entry = meta[alias.email.toLowerCase()]
        if (groupFilter === NO_GROUP && entry?.group_id) return false
        if (groupFilter !== ALL_GROUPS && groupFilter !== NO_GROUP && entry?.group_id !== groupFilter) {
          return false
        }
        if (!q) return true
        return (
          alias.email.toLowerCase().includes(q) ||
          alias.label.toLowerCase().includes(q) ||
          (entry?.note ?? '').toLowerCase().includes(q)
        )
      })
      .sort((left, right) => {
        const leftTime = dateTimestamp(left.alias.createdAt)
        const rightTime = dateTimestamp(right.alias.createdAt)

        // 没有有效时间的记录始终放在末尾，避免倒序时跳到列表顶部。
        if (leftTime === null || rightTime === null) {
          if (leftTime === rightTime) return left.index - right.index
          return leftTime === null ? 1 : -1
        }
        if (leftTime === rightTime) return left.index - right.index
        return sortDirection === 'asc' ? leftTime - rightTime : rightTime - leftTime
      })
      .map(({ alias }) => alias)
  }, [aliases, meta, search, filter, groupFilter, sortDirection])

  // 当前页的数据切片(过滤+排序之后)
  const safePage = Math.min(page, Math.max(1, Math.ceil(filtered.length / pageSize)))
  const pageAliases = filtered.slice((safePage - 1) * pageSize, safePage * pageSize)

  const activeCount = aliases.filter((a) => a.active).length
  const ungroupedCount = aliases.filter((a) => !meta[a.email.toLowerCase()]?.group_id).length

  // 选中集合里可能残留上一次操作删掉的 ID，统一用当前列表过滤一遍再使用，
  // 这样切换账号/刷新后不会出现"选中 N 个但只处理了 0 个"。
  const selectedAliases = useMemo(
    () => aliases.filter((a) => selected.has(a.anonymousId)),
    [aliases, selected],
  )
  const pageSelectedCount = pageAliases.filter((a) => selected.has(a.anonymousId)).length
  const allPageSelected = pageAliases.length > 0 && pageSelectedCount === pageAliases.length

  function toggleOne(id: string) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  /** 表头勾选框只管当前页：跨页勾选时不会因为翻页而丢掉之前的选中。 */
  function togglePage() {
    setSelected((prev) => {
      const next = new Set(prev)
      for (const alias of pageAliases) {
        if (allPageSelected) {
          next.delete(alias.anonymousId)
        } else {
          next.add(alias.anonymousId)
        }
      }
      return next
    })
  }

  async function runBulk(action: BulkAliasAction, groupId = '') {
    if (selectedAliases.length === 0) return
    const targets: BulkAliasTarget[] = selectedAliases.map((alias) => ({
      anonymous_id: alias.anonymousId,
      email: alias.email,
    }))
    setBusy(true)
    setActionError('')
    try {
      const result = await request<BulkAliasResult>('/api/alias-bulk', {
        method: 'POST',
        body: JSON.stringify({
          account_id: accountId,
          action,
          aliases: targets,
          group_id: groupId,
        }),
      })
      const label = BULK_LABEL[action]
      if (result.failed === 0) {
        show(`已${label} ${result.succeeded} 个邮箱`)
        setSelected(new Set())
      } else {
        show(`${label}完成：成功 ${result.succeeded} 个，失败 ${result.failed} 个`)
        // 只保留失败项继续勾选，方便用户直接重试
        const failedIds = new Set(
          result.results
            .filter((item) => !item.ok && item.anonymous_id)
            .map((item) => item.anonymous_id as string),
        )
        setSelected(failedIds)
        // 只列前几条，避免一大片红字把页面顶下去
        const reasons = result.results
          .filter((item) => !item.ok)
          .slice(0, 3)
          .map((item) => `${item.email || item.anonymous_id}：${item.error ?? '未知错误'}`)
        setActionError(`${result.failed} 个邮箱处理失败。${reasons.join('；')}`)
      }
      setConfirm(null)
      setRetryKey((k) => k + 1)
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
      setBulkGroup('')
    }
  }

  /** 导出当前筛选结果（不是仅当前页），方便对账、迁移或给表格软件分析。 */
  function exportCSV() {
    if (filtered.length === 0) {
      show('当前没有可导出的邮箱')
      return
    }
    const accountName = accounts.find((a) => a.id === accountId)?.name ?? accountId
    const rows = [
      ['邮箱', '标签', '分组', '备注', '状态', '创建时间', '账号'],
      ...filtered.map((alias) => {
        const entry = meta[alias.email.toLowerCase()]
        const group = entry?.group_id ? groupById.get(entry.group_id) : undefined
        return [
          alias.email,
          alias.label,
          group?.name ?? '未分组',
          entry?.note ?? '',
          alias.active ? '已启用' : '已停用',
          formatDate(alias.createdAt),
          accountName,
        ]
      }),
    ]
    downloadCSV(timestampedName('icloud-邮箱'), rows)
    show(`已导出 ${filtered.length} 条`)
  }

  function handleRetry() {
    setLoading(true)
    setRetryKey((k) => k + 1)
  }

  const copyEmail = useCallback(
    async (email: string) => {
      if (await copyText(email)) {
        show('邮箱已复制')
      } else {
        showCopyable(email, '复制失败，请手动复制')
      }
    },
    [show, showCopyable],
  )

  async function runAction(type: BulkConfirmAction) {
    if (!confirm || 'bulk' in confirm) return
    setBusy(true)
    setActionError('')
    const { alias } = confirm
    try {
      if (type === 'delete') {
        await request(
          `/api/aliases/${encodeURIComponent(alias.anonymousId)}`,
          {
            method: 'DELETE',
            body: JSON.stringify({ account_id: accountId, email: alias.email }),
          },
        )
        show('别名已删除')
      } else {
        await request(
          `/api/aliases/${encodeURIComponent(alias.anonymousId)}/${type === 'deactivate' ? 'deactivate' : 'reactivate'}`,
          { method: 'POST', body: JSON.stringify({ account_id: accountId }) },
        )
        show(type === 'deactivate' ? '别名已停用' : '别名已激活')
      }
      setConfirm(null)
      setRetryKey((k) => k + 1)
    } catch (err) {
      setActionError(
        err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态',
      )
    } finally {
      setBusy(false)
    }
  }

  function handleCreated(email: string) {
    setCreateOpen(false)
    showCopyable(email)
    setRetryKey((k) => k + 1)
  }

  if (accounts.length === 0 && !loading && !error) {
    return <p className="empty-state">暂无账号，请先到「账号」页面添加账号</p>
  }

  const isBulkConfirm = confirm !== null && 'bulk' in confirm
  const confirmTitle = confirm
    ? `${isBulkConfirm ? '批量' : ''}${
        confirm.type === 'delete' ? '删除邮箱' : confirm.type === 'deactivate' ? '停用邮箱' : '激活邮箱'
      }`
    : ''

  const confirmLabel =
    confirm?.type === 'delete' ? '确认删除' : confirm?.type === 'deactivate' ? '确认停用' : '确认激活'

  // 单条删除沿用"输入完整邮箱"的防误删流程；批量删除没法逐条核对，只做一次普通确认。
  const confirmRequireText =
    confirm && !('bulk' in confirm) && confirm.type === 'delete' ? confirm.alias.email : undefined

  const confirmMessage = (() => {
    if (!confirm) return ''
    if ('bulk' in confirm) {
      const n = selectedAliases.length
      if (confirm.type === 'delete') {
        return `将删除选中的 ${n} 个邮箱。此操作不可恢复，且不会影响 Apple 账号本身。`
      }
      if (confirm.type === 'deactivate') {
        return `将停用选中的 ${n} 个邮箱，之后它们将不再接收邮件。`
      }
      return `将重新激活选中的 ${n} 个邮箱。`
    }
    const email = confirm.alias.email
    if (confirm.type === 'delete') {
      return `将删除邮箱 ${email}。此操作不可恢复，且不会影响 Apple 账号本身。`
    }
    if (confirm.type === 'deactivate') {
      return `将停用邮箱 ${email}，之后该邮箱将不再接收邮件。`
    }
    return `将重新激活邮箱 ${email}。`
  })()

  return (
    <section>
      <PageHeader
        title="邮箱管理"
        actions={
          <>
            <button onClick={exportCSV} disabled={filtered.length === 0} title="导出当前筛选结果">
              <IconDownload size={16} />
              导出 CSV
            </button>
            <label className="inline-label" htmlFor="alias-account">
              账号
            </label>
            <select
              id="alias-account"
              value={accountId}
              onChange={(e) => {
                setAccountId(e.target.value)
                // 换账号后列表内容变了,回到第一页避免停在越界页码
                setPage(1)
                setBulkGroup('')
                const next = new URLSearchParams(searchParams)
                next.set('account_id', e.target.value)
                setSearchParams(next, { replace: true })
              }}
              style={{ width: 'auto' }}
            >
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
            <button className="primary" onClick={() => setCreateOpen(true)} disabled={!accountId}>
              <IconPlus size={16} />
              创建邮箱
            </button>
          </>
        }
      />

      <p className="hint-block">
        共 {aliases.length} 个邮箱（启用 {activeCount} 个，未分组 {ungroupedCount} 个）。
        邮箱地址、标签由 iCloud 决定，不可修改；分组和备注是本系统的归类信息，只存在本地。
      </p>

      <div className="data-panel">
        <div className="toolbar">
          <div className="toolbar-field toolbar-grow">
            <label htmlFor="alias-search">搜索</label>
            <div className="search-input">
              <input
                id="alias-search"
                type="search"
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value)
                  setPage(1)
                }}
                placeholder="按邮箱、标签或备注搜索"
              />
              <IconSearch size={16} />
            </div>
          </div>
          <div className="toolbar-field">
            <label htmlFor="alias-group-filter">分组</label>
            <select
              id="alias-group-filter"
              value={groupFilter}
              onChange={(e) => setGroupFilter(e.target.value)}
              style={{ width: 'auto' }}
            >
              <option value={ALL_GROUPS}>全部分组</option>
              <option value={NO_GROUP}>未分组</option>
              {groups.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
            </select>
          </div>
          <div className="toolbar-field">
            <label htmlFor="alias-filter">状态</label>
            <select
              id="alias-filter"
              value={filter}
              onChange={(e) => {
                setFilter(e.target.value as 'all' | 'active' | 'inactive')
                setPage(1)
              }}
              style={{ width: 'auto' }}
            >
              <option value="all">全部</option>
              <option value="active">已启用</option>
              <option value="inactive">已停用</option>
            </select>
          </div>
        </div>

        {selectedAliases.length > 0 && (
          <div className="bulk-bar" role="region" aria-label="批量操作">
            <span className="bulk-count">
              已选 <strong>{selectedAliases.length}</strong> 个
              <button type="button" className="link-like" onClick={() => setSelected(new Set())}>
                取消选择
              </button>
            </span>
            <div className="bulk-actions">
              <select
                aria-label="批量归入分组"
                value={bulkGroup}
                onChange={(e) => {
                  const value = e.target.value
                  if (!value) return
                  setBulkGroup(value)
                  void runBulk('assign_group', value === NO_GROUP ? '' : value)
                }}
                disabled={busy}
                style={{ width: 'auto' }}
              >
                <option value="">归入分组…</option>
                <option value={NO_GROUP}>移出分组</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </select>
              <button disabled={busy} onClick={() => setConfirm({ type: 'deactivate', bulk: true })}>
                停用
              </button>
              <button disabled={busy} onClick={() => setConfirm({ type: 'reactivate', bulk: true })}>
                激活
              </button>
              <button
                className="danger"
                disabled={busy}
                onClick={() => setConfirm({ type: 'delete', bulk: true })}
              >
                <IconTrash size={14} />
                删除
              </button>
            </div>
          </div>
        )}

        <AsyncState
          loading={loading}
          error={error}
          empty={filtered.length === 0}
          emptyText={aliases.length === 0 ? '暂无邮箱' : '没有匹配的邮箱'}
          onRetry={handleRetry}
        >
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th className="col-check">
                    <input
                      type="checkbox"
                      aria-label={allPageSelected ? '取消全选当前页' : '全选当前页'}
                      checked={allPageSelected}
                      ref={(el) => {
                        // 部分选中时显示横线，提示"本页还有没选的"
                        if (el) el.indeterminate = pageSelectedCount > 0 && !allPageSelected
                      }}
                      onChange={togglePage}
                    />
                  </th>
                  <th>邮箱</th>
                  <th>标签</th>
                  <th>分组</th>
                  <th>状态</th>
                  <th aria-sort={sortDirection === 'asc' ? 'ascending' : 'descending'}>
                    <button
                      type="button"
                      className="table-sort-button"
                      onClick={() => setSortDirection((direction) => (direction === 'asc' ? 'desc' : 'asc'))}
                      aria-label={`创建时间排序：当前${sortDirection === 'asc' ? '正序' : '倒序'}，点击切换为${sortDirection === 'asc' ? '倒序' : '正序'}`}
                      title="点击切换创建时间排序"
                    >
                      <span>创建时间</span>
                      {sortDirection === 'asc' ? <IconChevronUp size={14} /> : <IconChevronDown size={14} />}
                    </button>
                  </th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {pageAliases.map((alias) => {
                  const entry = meta[alias.email.toLowerCase()]
                  const group = entry?.group_id ? groupById.get(entry.group_id) : undefined
                  const checked = selected.has(alias.anonymousId)
                  return (
                    <tr key={alias.anonymousId} className={checked ? 'row-selected' : undefined}>
                      <td className="col-check">
                        <input
                          type="checkbox"
                          aria-label={`选择 ${alias.email}`}
                          checked={checked}
                          onChange={() => toggleOne(alias.anonymousId)}
                        />
                      </td>
                      <td>
                        <button
                          type="button"
                          className="link-like"
                          onClick={() => void copyEmail(alias.email)}
                          title="复制邮箱"
                        >
                          {alias.email}
                        </button>
                        {entry?.note && <div className="cell-secondary">{entry.note}</div>}
                      </td>
                      <td>{alias.label || '—'}</td>
                      <td>
                        {group ? (
                          <span className="group-chip">
                            <span className="group-dot" data-color={group.color} aria-hidden="true" />
                            {group.name}
                          </span>
                        ) : (
                          <span className="cell-muted">未分组</span>
                        )}
                      </td>
                      <td>
                        <span className={alias.active ? 'badge badge-active' : 'badge badge-neutral'}>
                          {alias.active ? <IconCheck size={12} /> : <IconClock size={12} />}
                          {alias.active ? '已启用' : '已停用'}
                        </span>
                      </td>
                      <td>{formatDate(alias.createdAt)}</td>
                      <td>
                        <div className="row-actions">
                          <button disabled={busy} onClick={() => setEditing(alias)} title="编辑分组与备注">
                            <IconEdit size={14} />
                            编辑
                          </button>
                          {alias.active ? (
                            <button
                              disabled={busy}
                              onClick={() => setConfirm({ type: 'deactivate', alias })}
                            >
                              停用
                            </button>
                          ) : (
                            <button
                              disabled={busy}
                              onClick={() => setConfirm({ type: 'reactivate', alias })}
                            >
                              激活
                            </button>
                          )}
                          <Link
                            to={`/inbox?account_id=${encodeURIComponent(accountId)}&alias=${encodeURIComponent(alias.email)}`}
                            title="查看此别名的收件箱"
                          >
                            <IconInbox size={14} />
                            收件箱
                          </Link>
                          <button
                            className="danger"
                            disabled={busy}
                            onClick={() => setConfirm({ type: 'delete', alias })}
                          >
                            <IconTrash size={14} />
                            删除
                          </button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </AsyncState>
        <Pagination
          page={safePage}
          pageSize={pageSize}
          total={filtered.length}
          onPageChange={setPage}
          onPageSizeChange={(size) => {
            setPageSize(size)
            setPage(1)
          }}
          showWhenEmpty
        />
      </div>

      <CreateAliasDialog
        accountId={accountId}
        open={createOpen}
        groups={groups}
        defaultGroupId={groupFilter !== ALL_GROUPS && groupFilter !== NO_GROUP ? groupFilter : ''}
        onClose={() => setCreateOpen(false)}
        onCreated={handleCreated}
      />

      {editing && (
        <AliasEditDialog
          key={editing.anonymousId}
          open
          accountId={accountId}
          email={editing.email}
          groupId={meta[editing.email.toLowerCase()]?.group_id}
          note={meta[editing.email.toLowerCase()]?.note}
          groups={groups}
          onClose={() => setEditing(null)}
          onSaved={(message) => {
            setEditing(null)
            show(message)
            setRetryKey((k) => k + 1)
          }}
        />
      )}

      {confirm && (
        <ConfirmDialog
          title={confirmTitle}
          message={confirmMessage}
          confirmLabel={confirmLabel}
          requireText={confirmRequireText}
          requireLabel={confirmRequireText ? '输入完整邮箱' : undefined}
          open
          busy={busy}
          onClose={() => setConfirm(null)}
          onConfirm={() =>
            'bulk' in confirm ? void runBulk(confirm.type) : void runAction(confirm.type)
          }
        />
      )}

      {actionError && (
        <div className="alert-error" role="alert" style={{ marginTop: 16 }}>
          {actionError}
        </div>
      )}
    </section>
  )
}

import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { Group } from '../api/types'
import AsyncState from '../components/AsyncState'
import ConfirmDialog from '../components/ConfirmDialog'
import GroupDialog from '../components/GroupDialog'
import PageHeader from '../components/PageHeader'
import { useToast } from '../components/ToastProvider'
import { IconEdit, IconPlus, IconTrash } from '../components/icons'

/**
 * 分组管理页。
 *
 * 分组是本系统自己的归类方式：iCloud 只认「邮箱 + 标签 + 启用状态 + 创建时间」，
 * 没有分组概念，所以这里的数据全部落在本地 alias_meta.json。
 */
export default function GroupsPage() {
  const [groups, setGroups] = useState<Group[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const [dialog, setDialog] = useState<{ open: boolean; group?: Group | null }>({ open: false })
  const [confirm, setConfirm] = useState<Group | null>(null)
  const [busy, setBusy] = useState(false)
  const { show } = useToast()

  useEffect(() => {
    let cancelled = false
    request<{ groups: Group[] }>('/api/groups')
      .then((data) => {
        if (cancelled) return
        setGroups(data.groups ?? [])
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

  async function handleDelete() {
    if (!confirm) return
    setBusy(true)
    try {
      await request(`/api/groups/${encodeURIComponent(confirm.id)}`, { method: 'DELETE' })
      show('分组已删除，组内邮箱已变为未分组')
      setConfirm(null)
      setLoading(true)
      setRetryKey((k) => k + 1)
    } catch (err) {
      show(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
    }
  }

  return (
    <section>
      <PageHeader
        title="分组"
        actions={
          <>
            <span className="page-hint">共 {groups.length} 个分组</span>
            <button className="primary" onClick={() => setDialog({ open: true, group: null })}>
              <IconPlus size={16} />
              新建分组
            </button>
          </>
        }
      />

      <AsyncState
        loading={loading}
        error={error}
        empty={groups.length === 0}
        emptyText="还没有分组，点右上角新建一个"
        onRetry={() => {
          setLoading(true)
          setRetryKey((k) => k + 1)
        }}
      >
        <div className="group-grid">
          {groups.map((group) => (
            <article className="group-card" key={group.id}>
              <div className="group-card-head">
                <span className="group-dot" data-color={group.color} aria-hidden="true" />
                <h3>{group.name}</h3>
                <span className="group-count">{group.alias_count}</span>
              </div>
              {group.note ? (
                <p className="group-note">{group.note}</p>
              ) : (
                <p className="group-note group-note-empty">未填写备注</p>
              )}
              <div className="group-card-actions">
                <Link className="btn-link" to={`/aliases?group_id=${encodeURIComponent(group.id)}`}>
                  查看邮箱
                </Link>
                <button onClick={() => setDialog({ open: true, group })}>
                  <IconEdit size={14} />
                  编辑
                </button>
                <button className="danger" disabled={busy} onClick={() => setConfirm(group)}>
                  <IconTrash size={14} />
                  删除
                </button>
              </div>
            </article>
          ))}
        </div>
      </AsyncState>

      <GroupDialog
        key={dialog.group?.id ?? 'new'}
        open={dialog.open}
        group={dialog.group}
        onClose={() => setDialog({ open: false })}
        onSaved={(message) => {
          setDialog({ open: false })
          show(message)
          setLoading(true)
          setRetryKey((k) => k + 1)
        }}
      />

      {confirm && (
        <ConfirmDialog
          title="删除分组"
          message={
            confirm.alias_count > 0
              ? `删除分组「${confirm.name}」后，组内 ${confirm.alias_count} 个邮箱会变为未分组。邮箱本身仍保留在 iCloud，不受影响。`
              : `删除分组「${confirm.name}」。该分组下没有邮箱。`
          }
          confirmLabel="确认删除"
          open
          busy={busy}
          onClose={() => setConfirm(null)}
          onConfirm={() => void handleDelete()}
        />
      )}
    </section>
  )
}

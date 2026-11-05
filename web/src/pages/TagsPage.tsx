import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { Tag, TagKind } from '../api/types'
import AsyncState from '../components/AsyncState'
import ConfirmDialog from '../components/ConfirmDialog'
import PageHeader from '../components/PageHeader'
import TagChip from '../components/TagChip'
import TagDialog from '../components/TagDialog'
import { useToast } from '../components/ToastProvider'
import { IconEdit, IconPlus, IconTrash } from '../components/icons'

/** 三个分区的展示顺序与文案。 */
const SECTIONS: { kind: TagKind; title: string; hint: string }[] = [
  { kind: 'purpose', title: '用途', hint: '这些邮箱是拿来干什么的' },
  { kind: 'owner', title: '使用者', hint: '这些邮箱归谁用' },
  { kind: 'other', title: '其它', hint: '不属于上面两类的补充标记' },
]

/**
 * 标注管理页。
 *
 * 为什么要单独一套标注：iCloud 的标签（label）在建别名时写死，之后不能改，
 * 而且它在 Apple 那边；实际最常变的信息恰恰是"这个号干什么用、谁在用"。
 * 所以本系统自己维护一份可编辑、可复用的标注目录，一个标注能挂到多个邮箱上。
 */
export default function TagsPage() {
  const [tags, setTags] = useState<Tag[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const [dialog, setDialog] = useState<{ open: boolean; tag?: Tag | null; kind?: TagKind }>({
    open: false,
  })
  const [confirm, setConfirm] = useState<Tag | null>(null)
  const [busy, setBusy] = useState(false)
  const { show } = useToast()

  useEffect(() => {
    let cancelled = false
    request<{ tags: Tag[] }>('/api/tags')
      .then((data) => {
        if (cancelled) return
        setTags(data.tags ?? [])
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

  function reload() {
    setLoading(true)
    setRetryKey((k) => k + 1)
  }

  async function handleDelete() {
    if (!confirm) return
    setBusy(true)
    try {
      const res = await request<{ deleted: boolean; affected: number }>(
        `/api/tags/${encodeURIComponent(confirm.id)}`,
        { method: 'DELETE' },
      )
      show(
        res.affected > 0
          ? `标注已删除，已从 ${res.affected} 个邮箱上摘掉`
          : '标注已删除',
      )
      setConfirm(null)
      reload()
    } catch (err) {
      show(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
    }
  }

  const total = tags.length

  return (
    <section>
      <PageHeader
        title="标注"
        actions={
          <>
            <span className="page-hint">共 {total} 个标注</span>
            <button className="primary" onClick={() => setDialog({ open: true, tag: null })}>
              <IconPlus size={16} />
              新建标注
            </button>
          </>
        }
      />

      <AsyncState
        loading={loading}
        error={error}
        empty={total === 0}
        emptyText="还没有标注，点右上角新建一个"
        onRetry={reload}
      >
        {SECTIONS.map((section) => {
          const items = tags.filter((t) => t.kind === section.kind)
          return (
            <div className="tag-section" key={section.kind}>
              <div className="tag-section-head">
                <h2>{section.title}</h2>
                <span className="page-hint">{section.hint}</span>
                <span className="spacer" />
                <button
                  className="btn-link"
                  onClick={() => setDialog({ open: true, tag: null, kind: section.kind })}
                >
                  <IconPlus size={14} />
                  在此新建
                </button>
              </div>
              {items.length === 0 ? (
                <p className="tag-section-empty">还没有「{section.title}」类标注</p>
              ) : (
                <div className="tag-grid">
                  {items.map((tag) => (
                    <article className="tag-card" key={tag.id}>
                      <div className="tag-card-head">
                        <TagChip tag={tag} />
                        <span className="spacer" />
                        <span className="tag-usage" title="有多少个邮箱挂着这个标注">
                          {tag.alias_count ?? 0} 个邮箱
                        </span>
                      </div>
                      {tag.note ? (
                        <p className="tag-note">{tag.note}</p>
                      ) : (
                        <p className="tag-note tag-note-empty">未填写说明</p>
                      )}
                      <div className="tag-card-actions">
                        <Link
                          className="btn-link"
                          to={`/aliases?tag_id=${encodeURIComponent(tag.id)}`}
                        >
                          查看邮箱
                        </Link>
                        <button onClick={() => setDialog({ open: true, tag })}>
                          <IconEdit size={14} />
                          编辑
                        </button>
                        <button className="danger" disabled={busy} onClick={() => setConfirm(tag)}>
                          <IconTrash size={14} />
                          删除
                        </button>
                      </div>
                    </article>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </AsyncState>

      <TagDialog
        key={`${dialog.tag?.id ?? 'new'}-${dialog.kind ?? ''}`}
        open={dialog.open}
        tag={dialog.tag}
        defaultKind={dialog.kind}
        onClose={() => setDialog({ open: false })}
        onSaved={(message) => {
          setDialog({ open: false })
          show(message)
          reload()
        }}
      />

      {confirm && (
        <ConfirmDialog
          title="删除标注"
          message={
            confirm.alias_count
              ? `删除标注「${confirm.name}」后，${confirm.alias_count} 个邮箱上的这个标注会被摘掉。邮箱本身和 iCloud 数据都不受影响。`
              : `删除标注「${confirm.name}」。当前没有邮箱使用它。`
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

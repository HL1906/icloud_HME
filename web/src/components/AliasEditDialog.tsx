import { useState } from 'react'
import Dialog from './Dialog'
import { request, ApiError } from '../api/client'
import type { Group, Tag, TagKind } from '../api/types'
import { IconLock } from './icons'

interface AliasEditDialogProps {
  open: boolean
  accountId: string
  email: string
  /** 当前分组（空 = 未分组） */
  groupId?: string
  note?: string
  groups: Group[]
  /** 全部可选标注（标注目录） */
  tags?: Tag[]
  /** 该邮箱当前挂着的标注 ID */
  tagIds?: string[]
  onClose: () => void
  onSaved: (message: string) => void
}

/** 标注在对话框里按分类分块展示。 */
const KIND_SECTIONS: { kind: TagKind; label: string }[] = [
  { kind: 'purpose', label: '用途' },
  { kind: 'owner', label: '使用者' },
  { kind: 'other', label: '其它' },
]

/**
 * 编辑别名 —— 只编辑本系统的信息（分组、备注、标注）。
 *
 * 邮箱地址由 iCloud 分配，无法修改；iCloud 的标签也是建号时写死、之后不可改，
 * 所以"这个号干什么用、谁在用"这类信息走本系统自带的标注（Tag）。
 */
export default function AliasEditDialog({
  open,
  accountId,
  email,
  groupId,
  note,
  groups,
  tags = [],
  tagIds = [],
  onClose,
  onSaved,
}: AliasEditDialogProps) {
  const [selectedGroup, setSelectedGroup] = useState(groupId ?? '')
  const [localNote, setLocalNote] = useState(note ?? '')
  const [selectedTags, setSelectedTags] = useState<string[]>(tagIds)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  function toggleTag(id: string) {
    setSelectedTags((prev) => (prev.includes(id) ? prev.filter((t) => t !== id) : [...prev, id]))
  }

  async function handleSubmit() {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await request('/api/alias-meta', {
        method: 'PUT',
        body: JSON.stringify({
          email,
          account_id: accountId,
          group_id: selectedGroup,
          note: localNote.trim(),
        }),
      })
      // 标注是另一条独立接口:分组/备注保存成功后再覆盖标注,
      // 这样某一边失败时提示能明确指向是哪一步。
      await request('/api/alias-tags', {
        method: 'PUT',
        body: JSON.stringify({ email, tag_ids: selectedTags }),
      })
      onSaved('已保存分组、备注与标注')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog title="编辑邮箱" open={open} onClose={onClose}>
      {error && (
        <div className="alert-error" role="alert">
          {error}
        </div>
      )}

      <div className="form-field">
        <label htmlFor="alias-edit-email">邮箱</label>
        <input id="alias-edit-email" value={email} readOnly disabled />
        <span className="hint">
          <IconLock size={12} /> 地址由 iCloud 分配，创建后不可修改。
        </span>
      </div>

      <div className="form-field">
        <label htmlFor="alias-edit-group">分组</label>
        <select
          id="alias-edit-group"
          value={selectedGroup}
          onChange={(e) => setSelectedGroup(e.target.value)}
        >
          <option value="">未分组</option>
          {groups.map((g) => (
            <option key={g.id} value={g.id}>
              {g.name}
            </option>
          ))}
        </select>
        <span className="hint">分组是本系统的归类方式，不会同步到 iCloud。</span>
      </div>

      <div className="form-field">
        <label htmlFor="alias-edit-note">备注（可选）</label>
        <input
          id="alias-edit-note"
          value={localNote}
          maxLength={200}
          placeholder="例如：注册了哪个站、打算什么时候弃用"
          onChange={(e) => setLocalNote(e.target.value.slice(0, 200))}
        />
      </div>

      <div className="form-field">
        {/* 这是一组切换按钮的分组标题,不是某个表单控件的标签,
            语义上不能用 <label>(label 必须绑定控件),用带样式的 span */}
        <span className="form-group-label">标注</span>
        {tags.length === 0 ? (
          <span className="hint">
            还没有标注。iCloud 的标签建号后不能改，所以另有一套可编辑的标注，
            去「标注」页新建几个再来分配。
          </span>
        ) : (
          <>
            {KIND_SECTIONS.map((section) => {
              const items = tags.filter((t) => t.kind === section.kind)
              if (items.length === 0) return null
              return (
                <div className="tag-picker-group" key={section.kind}>
                  <span className="tag-picker-label">{section.label}</span>
                  <div className="tag-picker">
                    {items.map((tag) => {
                      const active = selectedTags.includes(tag.id)
                      return (
                        <button
                          type="button"
                          key={tag.id}
                          className={active ? 'tag-option tag-option-on' : 'tag-option'}
                          aria-pressed={active}
                          onClick={() => toggleTag(tag.id)}
                          title={tag.note || tag.name}
                        >
                          <span className="tag-chip-dot" data-color={tag.color} aria-hidden="true" />
                          {tag.name}
                        </button>
                      )
                    })}
                  </div>
                </div>
              )
            })}
            <span className="hint">
              可以多选，也可以一个都不选。标注只存在本系统，不影响邮箱地址和 iCloud 数据。
            </span>
          </>
        )}
      </div>

      <div className="form-actions">
        <button onClick={onClose} disabled={busy}>
          取消
        </button>
        <button className="primary" onClick={() => void handleSubmit()} disabled={busy}>
          {busy ? '保存中…' : '保存'}
        </button>
      </div>
    </Dialog>
  )
}

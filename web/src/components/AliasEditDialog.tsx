import { useState } from 'react'
import Dialog from './Dialog'
import { request, ApiError } from '../api/client'
import type { Group } from '../api/types'
import { IconLock } from './icons'

interface AliasEditDialogProps {
  open: boolean
  accountId: string
  email: string
  /** 当前分组（空 = 未分组） */
  groupId?: string
  note?: string
  groups: Group[]
  onClose: () => void
  onSaved: (message: string) => void
}

/**
 * 编辑别名 —— 只编辑本系统的信息（分组、备注）。
 *
 * 邮箱地址由 iCloud 分配，无法修改；标签也是 iCloud 侧字段，
 * 我们的接口没有修改能力，因此这两项只做展示。
 */
export default function AliasEditDialog({
  open,
  accountId,
  email,
  groupId,
  note,
  groups,
  onClose,
  onSaved,
}: AliasEditDialogProps) {
  const [selectedGroup, setSelectedGroup] = useState(groupId ?? '')
  const [localNote, setLocalNote] = useState(note ?? '')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

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
      onSaved('已保存分组与备注')
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

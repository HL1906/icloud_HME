import { useState } from 'react'
import Dialog from './Dialog'
import { request, ApiError } from '../api/client'
import type { Group } from '../api/types'
import { COLOR_LABELS, GROUP_COLORS } from '../utils/groupColors'

interface GroupDialogProps {
  open: boolean
  /** 传入表示编辑，不传表示新建 */
  group?: Group | null
  onClose: () => void
  onSaved: (message: string) => void
}

/**
 * 分组编辑对话框。
 * 分组是本系统自有的归类方式，不会写到 iCloud，也不会改变邮箱地址。
 */
export default function GroupDialog({ open, group, onClose, onSaved }: GroupDialogProps) {
  // 对话框由父级条件渲染，每次打开都是全新挂载，初值直接取自 props
  const [name, setName] = useState(group?.name ?? '')
  const [color, setColor] = useState(group?.color ?? 'blue')
  const [note, setNote] = useState(group?.note ?? '')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function handleSubmit() {
    if (busy) return
    if (!name.trim()) {
      setError('请输入分组名')
      return
    }
    setBusy(true)
    setError('')
    try {
      if (group) {
        await request(`/api/groups/${encodeURIComponent(group.id)}`, {
          method: 'PATCH',
          body: JSON.stringify({ name: name.trim(), color, note: note.trim() }),
        })
        onSaved('分组已更新')
      } else {
        await request('/api/groups', {
          method: 'POST',
          body: JSON.stringify({ name: name.trim(), color, note: note.trim() }),
        })
        onSaved('分组已创建')
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog title={group ? '编辑分组' : '新建分组'} open={open} onClose={onClose}>
      {error && (
        <div className="alert-error" role="alert">
          {error}
        </div>
      )}
      <div className="form-field">
        <label htmlFor="group-name">分组名</label>
        <input
          id="group-name"
          value={name}
          maxLength={40}
          placeholder="例如：购物、订阅、临时注册"
          onChange={(e) => setName(e.target.value.slice(0, 40))}
        />
      </div>
      <div className="form-field">
        <label htmlFor="group-color">颜色</label>
        <select id="group-color" value={color} onChange={(e) => setColor(e.target.value)}>
          {GROUP_COLORS.map((item) => (
            <option key={item} value={item}>
              {COLOR_LABELS[item]}
            </option>
          ))}
        </select>
      </div>
      <div className="form-field">
        <label htmlFor="group-note">备注（可选）</label>
        <input
          id="group-note"
          value={note}
          maxLength={200}
          placeholder="这个分组是干什么用的"
          onChange={(e) => setNote(e.target.value.slice(0, 200))}
        />
      </div>
      <p className="hint">
        分组只存在本系统，用于你自己归类；不会写入 iCloud，也不影响邮箱地址。
      </p>
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

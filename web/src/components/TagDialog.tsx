import { useState } from 'react'
import Dialog from './Dialog'
import { request, ApiError } from '../api/client'
import type { Tag, TagKind } from '../api/types'
import { COLOR_LABELS, GROUP_COLORS } from '../utils/groupColors'

interface TagDialogProps {
  open: boolean
  /** 传入表示编辑，不传表示新建 */
  tag?: Tag | null
  /** 新建时的默认分类（在某一分区里点"新建"时带上） */
  defaultKind?: TagKind
  onClose: () => void
  onSaved: (message: string) => void
}

const KIND_OPTIONS: { value: TagKind; label: string; hint: string }[] = [
  { value: 'purpose', label: '用途', hint: '这个邮箱拿来干什么，例如购物、订阅、临时注册' },
  { value: 'owner', label: '使用者', hint: '这个邮箱归谁用，例如自己、客户甲、运营组' },
  { value: 'other', label: '其它', hint: '不属于上面两类的补充标记' },
]

/**
 * 标注编辑对话框。
 *
 * 标注是 iCloud 标签之外的第二套标签：iCloud 的 label 建号后不可修改，
 * 而"用途、谁在用"这类信息会变，所以单独维护一份可编辑的目录。
 */
export default function TagDialog({ open, tag, defaultKind, onClose, onSaved }: TagDialogProps) {
  const [name, setName] = useState(tag?.name ?? '')
  const [kind, setKind] = useState<TagKind>(tag?.kind ?? defaultKind ?? 'purpose')
  const [color, setColor] = useState(tag?.color ?? 'blue')
  const [note, setNote] = useState(tag?.note ?? '')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const kindHint = KIND_OPTIONS.find((item) => item.value === kind)?.hint ?? ''

  async function handleSubmit() {
    if (busy) return
    if (!name.trim()) {
      setError('请输入标注名')
      return
    }
    setBusy(true)
    setError('')
    const payload = {
      name: name.trim(),
      kind,
      color,
      note: note.trim(),
    }
    try {
      if (tag) {
        await request(`/api/tags/${encodeURIComponent(tag.id)}`, {
          method: 'PATCH',
          body: JSON.stringify(payload),
        })
        onSaved('标注已更新')
      } else {
        await request('/api/tags', {
          method: 'POST',
          body: JSON.stringify(payload),
        })
        onSaved('标注已创建')
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog title={tag ? '编辑标注' : '新建标注'} open={open} onClose={onClose}>
      {error && (
        <div className="alert-error" role="alert">
          {error}
        </div>
      )}
      <div className="form-field">
        <label htmlFor="tag-name">标注名</label>
        <input
          id="tag-name"
          value={name}
          maxLength={40}
          placeholder="例如：购物、客户甲、长期订阅"
          onChange={(e) => setName(e.target.value.slice(0, 40))}
        />
      </div>
      <div className="form-field">
        <label htmlFor="tag-kind">分类</label>
        <select id="tag-kind" value={kind} onChange={(e) => setKind(e.target.value as TagKind)}>
          {KIND_OPTIONS.map((item) => (
            <option key={item.value} value={item.value}>
              {item.label}
            </option>
          ))}
        </select>
        <p className="hint">{kindHint}</p>
      </div>
      <div className="form-field">
        <label htmlFor="tag-color">颜色</label>
        <select id="tag-color" value={color} onChange={(e) => setColor(e.target.value)}>
          {GROUP_COLORS.map((item) => (
            <option key={item} value={item}>
              {COLOR_LABELS[item]}
            </option>
          ))}
        </select>
      </div>
      <div className="form-field">
        <label htmlFor="tag-note">说明（可选）</label>
        <input
          id="tag-note"
          value={note}
          maxLength={120}
          placeholder="补充一句话，方便以后判断"
          onChange={(e) => setNote(e.target.value.slice(0, 120))}
        />
      </div>
      <p className="hint">
        标注只存在本系统，不会写入 iCloud，也不影响邮箱地址和 iCloud 上的标签。
        <br />
        一个标注可以同时挂到多个邮箱上；删除标注只会把它从邮箱上摘掉，邮箱本身不受影响。
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

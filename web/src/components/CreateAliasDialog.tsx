import { useState } from 'react'
import Dialog from './Dialog'
import { request, ApiError } from '../api/client'
import type { Group } from '../api/types'

interface CreateAliasDialogProps {
  accountId: string
  open: boolean
  groups: Group[]
  /** 从分组页跳进来时预选的分组 */
  defaultGroupId?: string
  onClose: () => void
  onCreated: (email: string) => void
}

/**
 * 手动创建别名的对话框。
 *
 * 与自动补号的区别：手动创建让用户自己填标签（写给 iCloud 备注用），
 * 自动补号则完全不传标签，只按分组在本地归档。
 */
export default function CreateAliasDialog({
  accountId,
  open,
  groups,
  defaultGroupId = '',
  onClose,
  onCreated,
}: CreateAliasDialogProps) {
  const [label, setLabel] = useState('')
  const [groupId, setGroupId] = useState(defaultGroupId)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit() {
    if (submitting) return
    setSubmitting(true)
    setError('')
    try {
      const data = await request<{ email: string }>('/api/create', {
        method: 'POST',
        body: JSON.stringify({ account_id: accountId, label: label.trim(), group_id: groupId }),
      })
      setLabel('')
      onCreated(data.email)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      title="创建邮箱"
      open={open}
      onClose={() => {
        setLabel('')
        setError('')
        onClose()
      }}
    >
      {error && (
        <div className="alert-error" role="alert">
          {error}
        </div>
      )}
      <div className="form-field">
        <label htmlFor="alias-label">标签（可选）</label>
        <input
          id="alias-label"
          value={label}
          onChange={(e) => setLabel(e.target.value.slice(0, 200))}
          maxLength={200}
          placeholder="例如：购物、订阅"
        />
        <p className="hint">
          标签是 iCloud 侧的原生字段，最长 200 字符，创建后不可修改；留空时 iCloud 会记为
          「Created 日期 时间」。新建的邮箱默认就是启用状态。
        </p>
      </div>
      <div className="form-field">
        <label htmlFor="alias-group">分组（可选）</label>
        <select id="alias-group" value={groupId} onChange={(e) => setGroupId(e.target.value)}>
          <option value="">未分组</option>
          {groups.map((g) => (
            <option key={g.id} value={g.id}>
              {g.name}
            </option>
          ))}
        </select>
        <p className="hint">分组只存在本系统，方便你自己归类，不会写到 iCloud。</p>
      </div>
      <div className="form-actions">
        <button onClick={onClose}>取消</button>
        <button className="primary" onClick={() => void handleSubmit()} disabled={submitting}>
          {submitting ? '创建中…' : '创建'}
        </button>
      </div>
    </Dialog>
  )
}

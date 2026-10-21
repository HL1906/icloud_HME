import { useState } from 'react'
import Dialog from './Dialog'
import { request, ApiError } from '../api/client'

interface ChangePasswordDialogProps {
  open: boolean
  onClose: () => void
  onChanged?: () => void
}

/** 密码强度提示:返回 0-4 的等级与文案。 */
export function passwordStrength(pw: string): { level: number; text: string } {
  if (!pw) return { level: 0, text: '' }
  let score = 0
  if (pw.length >= 8) score++
  if (pw.length >= 12) score++
  if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) score++
  if (/\d/.test(pw)) score++
  if (/[^A-Za-z0-9]/.test(pw)) score++
  const level = Math.min(4, score)
  const texts = ['很弱', '弱', '一般', '较强', '很强']
  return { level, text: texts[level] }
}

export default function ChangePasswordDialog({ open, onClose, onChanged }: ChangePasswordDialogProps) {
  const [oldPassword, setOldPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const strength = passwordStrength(newPassword)

  function reset() {
    setOldPassword('')
    setNewPassword('')
    setConfirmPassword('')
    setError('')
  }

  function close() {
    reset()
    onClose()
  }

  async function submit() {
    if (busy) return
    setError('')
    if (!oldPassword) {
      setError('请输入当前密码')
      return
    }
    if (newPassword.length < 8) {
      setError('新密码至少 8 位')
      return
    }
    if (newPassword !== confirmPassword) {
      setError('两次输入的新密码不一致')
      return
    }
    setBusy(true)
    try {
      await request('/api/auth/password', {
        method: 'POST',
        body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
      })
      reset()
      onChanged?.()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '修改失败，请稍后重试')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog title="修改密码" open={open} onClose={close}>
      {error && (
        <div className="alert-error" role="alert">
          {error}
        </div>
      )}
      <div className="form-field">
        <label htmlFor="old-password">当前密码</label>
        <input
          id="old-password"
          type="password"
          autoComplete="current-password"
          value={oldPassword}
          onChange={(e) => setOldPassword(e.target.value)}
        />
      </div>
      <div className="form-field">
        <label htmlFor="new-password">新密码</label>
        <input
          id="new-password"
          type="password"
          autoComplete="new-password"
          value={newPassword}
          onChange={(e) => setNewPassword(e.target.value)}
          placeholder="至少 8 位，建议字母+数字"
        />
        {newPassword && (
          <div className="pw-meter" data-level={strength.level}>
            <span className="pw-meter-bar" />
            <span className="pw-meter-bar" />
            <span className="pw-meter-bar" />
            <span className="pw-meter-bar" />
            <span className="pw-meter-text">{strength.text}</span>
          </div>
        )}
      </div>
      <div className="form-field">
        <label htmlFor="confirm-password">确认新密码</label>
        <input
          id="confirm-password"
          type="password"
          autoComplete="new-password"
          value={confirmPassword}
          onChange={(e) => setConfirmPassword(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void submit()
          }}
        />
      </div>
      <p className="hint">
        修改成功后，其他已登录的会话会被强制退出，当前会话保持登录。
      </p>
      <div className="form-actions">
        <button type="button" onClick={close} disabled={busy}>
          取消
        </button>
        <button type="button" className="primary" onClick={() => void submit()} disabled={busy}>
          {busy ? '保存中…' : '确认修改'}
        </button>
      </div>
    </Dialog>
  )
}

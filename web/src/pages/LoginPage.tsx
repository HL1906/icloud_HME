import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import { ApiError } from '../api/client'
import { IconEye, IconEyeOff, IconLock, IconShield } from '../components/icons'

export default function LoginPage() {
  const { login } = useAuth()
  const navigate = useNavigate()
  const [password, setPassword] = useState('')
  const [showPwd, setShowPwd] = useState(false)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const passwordRef = useRef<HTMLInputElement>(null)

  // 登录页只有一个输入框,进入即聚焦(比 autoFocus 更可控,a11y 规则也允许)
  useEffect(() => {
    passwordRef.current?.focus()
  }, [])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (submitting || !password) return
    setSubmitting(true)
    setError('')
    try {
      await login(password)
      navigate('/dashboard', { replace: true })
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="login-page">
      <div className="login-card">
        <div className="login-brand">
          <span className="logo" aria-hidden="true">
            <IconShield size={22} />
          </span>
          <h1>iCloud 隐藏邮箱管理台</h1>
        </div>

        <form onSubmit={handleSubmit} className="login-form">
          {error && (
            <div className="alert-error" role="alert">
              {error}
            </div>
          )}

          <div className="form-field">
            <label htmlFor="admin-password">管理员密码</label>
            <div className="password-field">
              <span className="password-icon" aria-hidden="true">
                <IconLock size={15} />
              </span>
              <input
                id="admin-password"
                ref={passwordRef}
                type={showPwd ? 'text' : 'password'}
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
                placeholder="请输入管理员密码"
              />
              <button
                type="button"
                className="password-toggle"
                aria-label={showPwd ? '隐藏密码' : '显示密码'}
                onClick={() => setShowPwd((v) => !v)}
                tabIndex={-1}
              >
                {showPwd ? <IconEyeOff size={15} /> : <IconEye size={15} />}
              </button>
            </div>
          </div>

          <button type="submit" className="primary login-submit" disabled={submitting || !password}>
            {submitting ? '登录中…' : '登录'}
          </button>
        </form>
      </div>

      <p className="login-footnote">仅限授权管理员访问 · 会话仅保存在本机</p>
    </div>
  )
}

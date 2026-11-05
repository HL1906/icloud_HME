import { useEffect, useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { request } from '../api/client'
import { useAuth } from '../auth/AuthProvider'
import ChangePasswordDialog from './ChangePasswordDialog'
import { useToast } from './ToastProvider'
import {
  IconAccounts,
  IconAliases,
  IconChevronDown,
  IconDashboard,
  IconGroups,
  IconInbox,
  IconKey,
  IconLogout,
  IconMenu,
  IconSettings,
  IconShield,
  IconTag,
} from './icons'

/** 主导航项。集中定义便于侧栏与移动端复用。 */
const NAV_ITEMS = [
  { to: '/dashboard', label: '总览', icon: IconDashboard },
  { to: '/aliases', label: '邮箱管理', icon: IconAliases },
  { to: '/groups', label: '分组', icon: IconGroups },
  { to: '/tags', label: '标注', icon: IconTag },
  { to: '/inbox', label: '收件箱', icon: IconInbox },
  { to: '/accounts', label: '账号', icon: IconAccounts },
  { to: '/settings', label: '设置', icon: IconSettings },
] as const

export default function AppShell() {
  const { logout } = useAuth()
  const navigate = useNavigate()
  const [navOpen, setNavOpen] = useState(false)
  const [pwdOpen, setPwdOpen] = useState(false)
  // 侧栏底部的账户菜单:管理员身份、修改密码、退出登录都收在这里
  const [accountOpen, setAccountOpen] = useState(false)
  const [expiresAt, setExpiresAt] = useState('')
  const { show } = useToast()

  useEffect(() => {
    let cancelled = false
    request<{ expires_at?: string }>('/api/auth/session')
      .then((data) => {
        if (!cancelled && data.expires_at) setExpiresAt(data.expires_at)
      })
      .catch(() => {
        // 忽略:拿不到过期时间就只显示"已登录"
      })
    return () => {
      cancelled = true
    }
  }, [])

  // 点空白处收起账户菜单(菜单里的按钮自己点完会关)
  useEffect(() => {
    if (!accountOpen) return
    const onClick = (e: MouseEvent) => {
      const el = e.target as HTMLElement | null
      if (!el?.closest('.sidebar-footer')) setAccountOpen(false)
    }
    document.addEventListener('click', onClick)
    return () => document.removeEventListener('click', onClick)
  }, [accountOpen])

  const sessionText = expiresAt ? `登录有效期至 ${formatShort(expiresAt)}` : '已登录'

  async function handleLogout() {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className={`app-shell${navOpen ? ' nav-open' : ''}`}>
      <a className="skip-link" href="#main-content">
        跳到主要内容
      </a>

      <aside className="sidebar" aria-label="侧边导航">
        <div className="sidebar-brand">
          <span className="brand-logo" aria-hidden="true">
            <IconShield size={16} />
          </span>
          <span className="brand-text">
            <strong>iCloud HME</strong>
            <small>邮箱管理台</small>
          </span>
        </div>

        <nav aria-label="主导航">
          {NAV_ITEMS.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              onClick={() => setNavOpen(false)}
              className={({ isActive }) => (isActive ? 'nav-item active' : 'nav-item')}
            >
              <item.icon size={16} />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </nav>

        <div className="sidebar-footer">
          {accountOpen && (
            <div className="account-menu" id="account-menu" role="menu">
              <div className="account-menu-head">
                <strong>管理员</strong>
                <small>{sessionText}</small>
              </div>
              <button
                type="button"
                role="menuitem"
                className="nav-item as-button"
                onClick={() => {
                  setAccountOpen(false)
                  setPwdOpen(true)
                }}
              >
                <IconKey size={16} />
                <span>修改密码</span>
              </button>
              <button
                type="button"
                role="menuitem"
                className="nav-item as-button is-danger"
                onClick={() => void handleLogout()}
              >
                <IconLogout size={16} />
                <span>退出登录</span>
              </button>
            </div>
          )}
          <button
            type="button"
            className={`account-trigger${accountOpen ? ' is-open' : ''}`}
            aria-expanded={accountOpen}
            aria-controls="account-menu"
            aria-haspopup="menu"
            onClick={() => setAccountOpen((v) => !v)}
          >
            <span className="account-avatar" aria-hidden="true">
              <IconShield size={14} />
            </span>
            <span className="account-info">
              <strong>管理员</strong>
              <small>{sessionText}</small>
            </span>
            <IconChevronDown size={14} className="account-caret" />
          </button>
        </div>
      </aside>

      <div className="app-body">
        <header className="topbar">
          <button
            type="button"
            className="icon-button menu-toggle"
            aria-label="展开导航"
            aria-expanded={navOpen}
            onClick={() => setNavOpen((v) => !v)}
          >
            <IconMenu size={18} />
          </button>
          <span className="topbar-title">iCloud 邮箱管理</span>
          <span className="spacer" />
        </header>

        <main id="main-content">
          <Outlet />
        </main>
      </div>

      {navOpen && <button type="button" className="nav-scrim" aria-label="收起导航" onClick={() => setNavOpen(false)} />}

      <ChangePasswordDialog
        open={pwdOpen}
        onClose={() => setPwdOpen(false)}
        onChanged={() => {
          setPwdOpen(false)
          show('密码已修改，其他会话已退出')
        }}
      />
    </div>
  )
}

/** 「登录有效期至 10-23 09:40」里的短时间格式,解析失败返回空串 */
function formatShort(raw: string): string {
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (v: number) => String(v).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

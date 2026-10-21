import { useState } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthProvider'
import ChangePasswordDialog from './ChangePasswordDialog'
import { useToast } from './ToastProvider'
import {
  IconAccounts,
  IconAliases,
  IconDashboard,
  IconInbox,
  IconKey,
  IconLogout,
  IconMenu,
  IconShield,
} from './icons'

/** 主导航项。集中定义便于侧栏与移动端复用。 */
const NAV_ITEMS = [
  { to: '/dashboard', label: '总览', icon: IconDashboard },
  { to: '/aliases', label: '隐藏邮箱', icon: IconAliases },
  { to: '/inbox', label: '收件箱', icon: IconInbox },
  { to: '/accounts', label: '账号', icon: IconAccounts },
] as const

export default function AppShell() {
  const { logout } = useAuth()
  const navigate = useNavigate()
  const [navOpen, setNavOpen] = useState(false)
  const [pwdOpen, setPwdOpen] = useState(false)
  const { show } = useToast()

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
            <small>隐藏邮箱管理台</small>
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
          <button type="button" className="nav-item as-button" onClick={() => setPwdOpen(true)}>
            <IconKey size={16} />
            <span>修改密码</span>
          </button>
          <button type="button" className="nav-item as-button" onClick={() => void handleLogout()}>
            <IconLogout size={16} />
            <span>退出登录</span>
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
          <span className="topbar-title">iCloud 隐藏邮箱</span>
          <span className="spacer" />
          <span className="topbar-badge" title="仅本机管理员可访问">
            <IconShield size={13} />
            管理员
          </span>
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

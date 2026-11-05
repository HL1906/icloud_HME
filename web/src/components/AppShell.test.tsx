import { http, HttpResponse } from 'msw'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import AppShell from './AppShell'
import { AuthProvider } from '../auth/AuthProvider'
import { ToastProvider } from './ToastProvider'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'

function renderShell() {
  return render(
    <MemoryRouter initialEntries={['/dashboard']}>
      <ToastProvider>
        <AuthProvider>
          <Routes>
            <Route element={<AppShell />}>
              <Route path="/dashboard" element={<p>总览内容</p>} />
            </Route>
            <Route path="/login" element={<p>登录页</p>} />
          </Routes>
        </AuthProvider>
      </ToastProvider>
    </MemoryRouter>,
  )
}

function mockSession(expiresAt = '2026-10-23T09:40:00+08:00') {
  server.use(
    http.get('/api/auth/session', () =>
      HttpResponse.json({
        success: true,
        data: { csrf_token: 'csrf-test', expires_at: expiresAt },
      }),
    ),
    http.post('/api/auth/logout', () => {
      loggedOut = true
      return HttpResponse.json({ success: true, data: { logged_out: true } })
    }),
  )
}

let loggedOut = false

beforeEach(() => {
  loggedOut = false
  setCSRFToken('csrf-test')
})

describe('AppShell 侧栏账户区', () => {
  it('管理员身份、修改密码、退出登录收在同一个入口里', async () => {
    mockSession()
    renderShell()

    // 收起时只露出身份和有效期
    const trigger = await screen.findByRole('button', { name: /管理员/ })
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(trigger).toHaveTextContent('登录有效期至 10-23 09:40')
    expect(screen.queryByRole('menuitem', { name: /修改密码/ })).not.toBeInTheDocument()

    // 展开后两项都在
    await userEvent.setup().click(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('menuitem', { name: /修改密码/ })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /退出登录/ })).toBeInTheDocument()
  })

  it('点「修改密码」打开改密弹窗', async () => {
    mockSession()
    renderShell()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: /管理员/ }))
    await user.click(screen.getByRole('menuitem', { name: /修改密码/ }))

    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })

  it('点「退出登录」调用登出接口并回到登录页', async () => {
    mockSession()
    renderShell()
    const user = userEvent.setup()

    await user.click(await screen.findByRole('button', { name: /管理员/ }))
    await user.click(screen.getByRole('menuitem', { name: /退出登录/ }))

    await waitFor(() => expect(loggedOut).toBe(true))
    expect(await screen.findByText('登录页')).toBeInTheDocument()
  })

  it('拿不到会话有效期时只显示「已登录」', async () => {
    mockSession('')
    renderShell()

    const trigger = await screen.findByRole('button', { name: /管理员/ })
    expect(trigger).toHaveTextContent('已登录')
    expect(trigger).not.toHaveTextContent('登录有效期至')
  })
})

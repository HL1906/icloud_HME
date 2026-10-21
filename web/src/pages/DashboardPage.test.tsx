import { http, HttpResponse } from 'msw'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import DashboardPage from './DashboardPage'
import { server } from '../test/server'
import { ToastProvider } from '../components/ToastProvider'
import type { AccountSummary } from '../api/types'

const accounts: AccountSummary[] = [
  {
    id: 'acc_1',
    name: '主号',
    real_email: 'a@example.com',
    icloud_email: 'a@icloud.com',
    host: 'icloud.com',
    status: 'active',
    alias_total: 2,
    alias_active: 2,
    has_cookies: true,
    has_app_password: true,
    has_proxy: false,
    last_validated: '2026-08-04T09:00:00+08:00',
    created_at: '2026-08-01T09:00:00+08:00',
  },
  {
    id: 'acc_2',
    name: '备用号',
    real_email: 'b@example.com',
    icloud_email: 'b@icloud.com',
    host: 'icloud.com',
    status: 'active',
    alias_total: 0,
    alias_active: 0,
    has_cookies: true,
    has_app_password: false,
    has_proxy: false,
    last_validated: '',
    created_at: '2026-08-02T09:00:00+08:00',
  },
]

const aliasesByAccount: Record<string, unknown[]> = {
  acc_1: [
    { email: 'alpha@icloud.com', anonymousId: 'a1', label: '发给 A', active: true, createdAt: '2026-08-03T10:00:00+08:00' },
    { email: 'beta@icloud.com', anonymousId: 'a2', label: '发给 B', active: false, createdAt: '2026-08-04T10:00:00+08:00' },
  ],
  acc_2: [],
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/dashboard']}>
      <Routes>
        <Route
          path="/dashboard"
          element={
            <ToastProvider>
              <DashboardPage />
            </ToastProvider>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('DashboardPage', () => {
  beforeEach(() => {
    server.resetHandlers()
  })

  it('汇总隐藏邮箱总数与启用/停用数量', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', ({ request }) => {
        const accountId = new URL(request.url).searchParams.get('account_id') ?? ''
        return HttpResponse.json({
          success: true,
          data: { account_id: accountId, count: aliasesByAccount[accountId].length, aliases: aliasesByAccount[accountId] },
        })
      }),
    )
    renderPage()

    await screen.findByText('alpha@icloud.com')
    expect(screen.getByText('隐藏邮箱总数')).toBeInTheDocument()
    // 共 2 个别名:1 个启用、1 个停用
    const totalCard = screen.getByText('隐藏邮箱总数').closest('.stat-card') as HTMLElement
    expect(totalCard).toHaveTextContent('2')
    const activeCard = screen.getByText('可接收邮件').closest('.stat-card') as HTMLElement
    expect(activeCard).toHaveTextContent('1')
  })

  it('单个账号读取别名失败不影响其它账号展示', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', ({ request }) => {
        const accountId = new URL(request.url).searchParams.get('account_id')
        if (accountId === 'acc_2') {
          return HttpResponse.json(
            { success: false, code: 'UPSTREAM_FAILURE', message: '获取别名列表失败' },
            { status: 502 },
          )
        }
        return HttpResponse.json({
          success: true,
          data: { account_id: accountId, count: 2, aliases: aliasesByAccount.acc_1 },
        })
      }),
    )
    renderPage()

    await screen.findByText('alpha@icloud.com')
    await waitFor(() => expect(screen.getByText('读取失败')).toBeInTheDocument())
    // 正常的账号仍然展示
    expect(screen.getByText('主号')).toBeInTheDocument()
  })

  it('没有账号时提示先添加账号', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: [] })),
    )
    renderPage()
    expect(await screen.findByText(/先到「账号」页面添加/)).toBeInTheDocument()
  })
})

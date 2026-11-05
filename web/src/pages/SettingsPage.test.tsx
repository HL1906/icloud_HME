import { http, HttpResponse } from 'msw'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import SettingsPage from './SettingsPage'
import { server } from '../test/server'
import { ToastProvider } from '../components/ToastProvider'
import type { AccountSummary, PoolConfig, SettingsResult } from '../api/types'

const settingsData: SettingsResult = {
  settings: {
    auto_revoke_enabled: true,
    auto_revoke_interval_min: 10,
    public_mail_hours: 24,
    public_mail_limit: 20,
    public_base_url: '',
  },
  env: {
    data_dir: '/var/lib/icloud-hme',
    public_base_url_env: '',
    version: '1.2.3',
    secure_cookie: true,
  },
}

const mainAccount: AccountSummary = {
  id: 'acc_1',
  name: '主号',
  real_email: 'owner@icloud.com',
  icloud_email: 'owner@icloud.com',
  host: 'icloud.com',
  status: 'active',
  alias_total: 30,
  alias_active: 28,
  has_cookies: true,
  has_app_password: true,
  has_proxy: false,
  last_validated: '2026-10-21T10:00:00+08:00',
  status_message: '',
  created_at: '2026-10-01T10:00:00+08:00',
}

const noCredAccount: AccountSummary = {
  ...mainAccount,
  id: 'acc_2',
  name: '备用号',
  real_email: 'second@qq.com',
  icloud_email: 'second@qq.com',
  status: 'pending',
  has_cookies: false,
  has_app_password: false,
  alias_total: 0,
  alias_active: 0,
}

const poolConfig: PoolConfig = {
  account_id: 'acc_1',
  enabled: true,
  target: 30,
  max_per_run: 3,
  interval_sec: 300,
  filter: { group_id: 'g_shop', state: 'stock', active: 'active' },
  last_check_at: '2026-10-21T10:00:00+08:00',
  last_current: 12,
  total_created: 12,
}

function renderPage() {
  return render(
    <MemoryRouter>
      <ToastProvider>
        <SettingsPage />
      </ToastProvider>
    </MemoryRouter>,
  )
}

function mockAll(overrides?: {
  settings?: SettingsResult
  configs?: PoolConfig[]
  accounts?: AccountSummary[]
  poolFails?: boolean
}) {
  server.use(
    http.get('/api/settings', () =>
      HttpResponse.json({ success: true, data: overrides?.settings ?? settingsData }),
    ),
    http.get('/api/pool', () =>
      overrides?.poolFails
        ? HttpResponse.json(
            { success: false, code: 'POOL_UNAVAILABLE', message: '自动补号功能未启用' },
            { status: 503 },
          )
        : HttpResponse.json({
            success: true,
            data: { configs: overrides?.configs ?? [poolConfig] },
          }),
    ),
    http.get('/api/accounts', () =>
      HttpResponse.json({
        success: true,
        data: overrides?.accounts ?? [mainAccount],
      }),
    ),
    http.get('/api/groups', () =>
      HttpResponse.json({
        success: true,
        data: {
          groups: [{ id: 'g_shop', name: '购物', color: 'amber', created_at: '', alias_count: 3 }],
        },
      }),
    ),
    http.get('/api/pool/:id/preview', () =>
      HttpResponse.json({
        success: true,
        data: {
          account_id: 'acc_1',
          current: 12,
          target: 30,
          to_create: 3,
          breakdown: { stock: 12, issued: 18, revoked: 0, inactive: 0, total: 30 },
        },
      }),
    ),
  )
}

describe('SettingsPage', () => {
  it('回填当前设置并展示自动补号规则', async () => {
    mockAll()
    renderPage()

    expect(await screen.findByLabelText('检查间隔（分钟）')).toHaveValue(10)
    expect(screen.getByLabelText('时间范围（小时）')).toHaveValue(24)
    expect(screen.getByLabelText('默认条数')).toHaveValue(20)
    expect(screen.getByLabelText('启用自动回收')).toBeChecked()

    // 补号卡片:账号、库存水位、规则、状态
    expect(screen.getByText('主号')).toBeInTheDocument()
    expect(screen.getByText('12')).toBeInTheDocument()
    expect(screen.getByText('/ 30')).toBeInTheDocument()
    expect(screen.getByText('购物')).toBeInTheDocument()
    expect(screen.getByText('仅启用中')).toBeInTheDocument()
    expect(screen.getByText('运行中')).toBeInTheDocument()
  })

  it('没配置补号的账号也会列出来,并标出缺凭据', async () => {
    mockAll({ accounts: [mainAccount, noCredAccount] })
    renderPage()

    expect(await screen.findByText('主号')).toBeInTheDocument()
    expect(screen.getByText('备用号')).toBeInTheDocument()
    expect(screen.getByText('未开启')).toBeInTheDocument()
    // 未配置的账号也有"开启补号"入口
    expect(screen.getByRole('button', { name: /开启补号/ })).toBeInTheDocument()
    // 没凭据的账号给出明确提示
    expect(screen.getByText(/补号只在有 Cookie 的账号上能跑通/)).toBeInTheDocument()
  })

  it('改设置后出现保存条,保存时提交整份设置', async () => {
    let putBody: Record<string, unknown> | null = null
    mockAll()
    server.use(
      http.put('/api/settings', async ({ request }) => {
        putBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ success: true, data: settingsData.settings })
      }),
    )
    renderPage()

    const interval = await screen.findByLabelText('检查间隔（分钟）')
    const user = userEvent.setup()
    await user.clear(interval)
    await user.type(interval, '30')
    await user.click(screen.getByLabelText('启用自动回收')) // 关掉

    // 有改动才出现保存条
    expect(await screen.findByText('设置已改动，尚未保存')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '保存设置' }))

    await waitFor(() =>
      expect(putBody).toMatchObject({
        auto_revoke_enabled: false,
        auto_revoke_interval_min: 30,
        public_mail_hours: 24,
        public_mail_limit: 20,
      }),
    )
    expect(await screen.findByText('设置已保存，立即生效')).toBeInTheDocument()
  })

  it('点「放弃」丢弃未保存的改动', async () => {
    mockAll()
    renderPage()

    const interval = await screen.findByLabelText('检查间隔（分钟）')
    const user = userEvent.setup()
    await user.clear(interval)
    await user.type(interval, '30')
    expect(await screen.findByText('设置已改动，尚未保存')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '放弃' }))
    await waitFor(() => expect(screen.getByLabelText('检查间隔（分钟）')).toHaveValue(10))
    expect(screen.queryByText('设置已改动，尚未保存')).not.toBeInTheDocument()
  })

  it('间隔填 0 时本地拦截,不发请求', async () => {
    let putCalled = false
    mockAll()
    server.use(
      http.put('/api/settings', () => {
        putCalled = true
        return HttpResponse.json({ success: true, data: settingsData.settings })
      }),
    )
    renderPage()

    const interval = await screen.findByLabelText('检查间隔（分钟）')
    const user = userEvent.setup()
    await user.clear(interval)
    await user.type(interval, '0')
    await user.click(screen.getByRole('button', { name: '保存设置' }))

    expect(await screen.findByText(/扫描间隔要填 1-1440/)).toBeInTheDocument()
    expect(putCalled).toBe(false)
  })

  it('一个账号都没有时提示去添加', async () => {
    mockAll({ configs: [], accounts: [] })
    renderPage()
    expect(await screen.findByText(/还没有账号，先到/)).toBeInTheDocument()
  })

  it('调度器没启用时补号卡片给出说明', async () => {
    mockAll({ poolFails: true })
    renderPage()
    expect(await screen.findByText(/自动补号功能未启用/)).toBeInTheDocument()
  })

  it('环境变量指定了对外域名时给出提示', async () => {
    mockAll({
      settings: {
        ...settingsData,
        env: { ...settingsData.env, public_base_url_env: 'https://env.example.com' },
      },
    })
    renderPage()

    const hint = await screen.findByText(/部署时已用环境变量指定了/)
    expect(hint).toBeInTheDocument()
    expect(within(hint.closest('span') as HTMLElement).getByText('https://env.example.com')).toBeInTheDocument()
  })
})

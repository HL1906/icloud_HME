import { http, HttpResponse } from 'msw'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import AliasesPage from './AliasesPage'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'
import { ToastProvider } from '../components/ToastProvider'
import type { AccountSummary, Alias } from '../api/types'

const accounts: AccountSummary[] = [
  {
    id: 'acc_1',
    name: '主号',
    real_email: 'a@example.com',
    icloud_email: 'a@icloud.com',
    host: 'icloud.com',
    status: 'active',
    alias_total: 2,
    alias_active: 1,
    has_cookies: true,
    has_app_password: false,
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
    alias_total: 1,
    alias_active: 1,
    has_cookies: true,
    has_app_password: false,
    has_proxy: false,
    last_validated: '2026-08-04T09:00:00+08:00',
    created_at: '2026-08-01T09:00:00+08:00',
  },
]

const aliases: Alias[] = [
  {
    email: 'alpha@icloud.com',
    anonymousId: 'anon_alpha',
    label: 'Alpha',
    active: true,
    createdAt: '2026-07-01T00:00:00+08:00',
  },
  {
    email: 'beta@icloud.com',
    anonymousId: 'anon_beta',
    label: 'Beta',
    active: false,
    createdAt: '2026-07-02T00:00:00+08:00',
  },
]

function renderPage(initialPath = '/aliases') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route
          path="/aliases"
          element={
            <ToastProvider>
              <AliasesPage />
            </ToastProvider>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('AliasesPage', () => {
  beforeEach(() => {
    setCSRFToken('csrf-test')
    server.resetHandlers()
  })

  it('无账号时显示引导', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: [] })),
    )
    renderPage()
    expect(await screen.findByText(/暂无账号/)).toBeInTheDocument()
  })

  it('账号切换:URL query 优先,回退到第一个账号', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', ({ request }) => {
        const url = new URL(request.url)
        const id = url.searchParams.get('account_id')
        return HttpResponse.json({
          success: true,
          data: {
            account_id: id,
            count: id === 'acc_2' ? 1 : 2,
            aliases: id === 'acc_2' ? [aliases[0]] : aliases,
          },
        })
      }),
    )
    const { unmount } = renderPage('/aliases?account_id=acc_2')
    expect(await screen.findByText('alpha@icloud.com')).toBeInTheDocument()
    expect(screen.queryByText('beta@icloud.com')).toBeNull()
    unmount()
    renderPage('/aliases?account_id=bad_id')
    // 回退到第一个账号 acc_1,显示 2 个别名
    expect(await screen.findByText('beta@icloud.com')).toBeInTheDocument()
  })

  it('loading/empty/error/retry 状态', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json(
          { success: false, code: 'UPSTREAM_FAILURE', message: '获取别名列表失败' },
          { status: 502 },
        ),
      ),
    )
    renderPage()
    const retry = await screen.findByRole('button', { name: /重试/ })
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({ success: true, data: { account_id: 'acc_1', count: 0, aliases: [] } }),
      ),
    )
    await userEvent.click(retry)
    expect(await screen.findByText(/暂无邮箱/)).toBeInTheDocument()
  })

  it('按 email/label 大小写不敏感搜索与 active 状态筛选', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({ success: true, data: { account_id: 'acc_1', count: 2, aliases } }),
      ),
    )
    renderPage()
    expect(await screen.findByText('alpha@icloud.com')).toBeInTheDocument()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/搜索/), 'ALPHA')
    expect(screen.getByText('alpha@icloud.com')).toBeInTheDocument()
    expect(screen.queryByText('beta@icloud.com')).toBeNull()
    await user.clear(screen.getByLabelText(/搜索/))
    await user.selectOptions(screen.getByLabelText(/状态/), 'active')
    expect(screen.getByText('alpha@icloud.com')).toBeInTheDocument()
    expect(screen.queryByText('beta@icloud.com')).toBeNull()
  })

  it('创建时间默认倒序且可切换正序,兼容时间戳并按收件箱格式显示', async () => {
    const timestampAliases: Alias[] = [
      { ...aliases[0], createdAt: '1787406420000' },
      { ...aliases[1], createdAt: '1787406360000' },
    ]
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: { account_id: 'acc_1', count: 2, aliases: timestampAliases },
        }),
      ),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const table = screen.getByRole('table')
    const emailButtons = () => within(table).getAllByRole('button', { name: /@icloud\.com/ })
    expect(emailButtons().map((button) => button.textContent)).toEqual([
      'alpha@icloud.com',
      'beta@icloud.com',
    ])
    expect(screen.getAllByText(/^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}$/)).toHaveLength(2)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /创建时间排序/ }))
    expect(emailButtons().map((button) => button.textContent)).toEqual([
      'beta@icloud.com',
      'alpha@icloud.com',
    ])

    const alphaRow = within(table).getByRole('row', { name: /alpha@icloud\.com/ })
    expect(within(alphaRow).getByRole('link', { name: /收件箱/ })).toHaveAttribute(
      'href',
      '/inbox?account_id=acc_1&alias=alpha%40icloud.com',
    )
  })

  it('创建邮箱:200 字符边界、成功后刷新并可复制邮箱', async () => {
    let created = false
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            count: created ? 3 : 2,
            aliases: created
              ? [...aliases, { email: 'gamma@icloud.com', anonymousId: 'anon_gamma', label: 'Gamma', active: true }]
              : aliases,
          },
        }),
      ),
      http.post('/api/create', async () => {
        created = true
        return HttpResponse.json({
          success: true,
          data: {
            email: 'gamma@icloud.com',
            label: 'Gamma',
            created_at: '2026-08-05T09:00:00+08:00',
            account_id: 'acc_1',
          },
        })
      }),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /创建邮箱/ }))
    // 200 字符边界:输入 201 字符被截断到 200
    await user.type(
      screen.getByLabelText(/标签/),
      'x'.repeat(201),
    )
    expect((screen.getByLabelText(/标签/) as HTMLInputElement).value.length).toBe(200)
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /创建/ }))
    expect(await screen.findByText('gamma@icloud.com')).toBeInTheDocument()
  })

  it('标签留空也能创建:iCloud 侧会补默认标签', async () => {
    let created = false
    let sentLabel: string | null = null
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            count: created ? 3 : 2,
            aliases: created
              ? [...aliases, { email: 'delta@icloud.com', anonymousId: 'anon_delta', label: '', active: true }]
              : aliases,
          },
        }),
      ),
      http.post('/api/create', async ({ request }) => {
        const body = (await request.json()) as { label?: string }
        sentLabel = body.label ?? null
        created = true
        return HttpResponse.json({
          success: true,
          data: {
            email: 'delta@icloud.com',
            label: 'Created 2026-10-21 17:05',
            created_at: '2026-10-21T17:05:00+08:00',
            account_id: 'acc_1',
          },
        })
      }),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /创建邮箱/ }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: /创建/ }))
    expect(await screen.findByText('delta@icloud.com')).toBeInTheDocument()
    expect(sentLabel).toBe('')
  })

  it('停用别名:显示目标邮箱并二次确认', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({ success: true, data: { account_id: 'acc_1', count: 2, aliases } }),
      ),
      http.post('/api/aliases/:id/deactivate', () =>
        HttpResponse.json({ success: true, data: { anonymous_id: 'anon_alpha', success: true } }),
      ),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()
    await user.click(screen.getAllByRole('button', { name: /停用/ })[0])
    expect(screen.getByRole('dialog')).toHaveTextContent('alpha@icloud.com')
    await user.click(screen.getByRole('button', { name: /确认停用/ }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('激活别名:显示目标邮箱并二次确认', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({ success: true, data: { account_id: 'acc_1', count: 2, aliases } }),
      ),
      http.post('/api/aliases/:id/reactivate', () =>
        HttpResponse.json({ success: true, data: { anonymous_id: 'anon_beta', success: true } }),
      ),
    )
    renderPage()
    await screen.findByText('beta@icloud.com')
    const user = userEvent.setup()
    await user.click(screen.getAllByRole('button', { name: /激活/ })[0])
    expect(screen.getByRole('dialog')).toHaveTextContent('beta@icloud.com')
    await user.click(screen.getByRole('button', { name: /确认激活/ }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('删除别名:要求输入完整邮箱,用 anonymousId 构造 URL 并编码', async () => {
    let deletedUrl = ''
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({ success: true, data: { account_id: 'acc_1', count: 2, aliases } }),
      ),
      http.delete('/api/aliases/:id', ({ request }) => {
        deletedUrl = request.url
        return HttpResponse.json({ success: true, data: { anonymous_id: 'anon_alpha' } })
      }),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()
    const alphaRow = within(screen.getByRole('table')).getByRole('row', { name: /alpha@icloud\.com/ })
    await user.click(within(alphaRow).getByRole('button', { name: /删除/ }))
    expect(screen.getByRole('dialog')).toHaveTextContent('alpha@icloud.com')
    // 输入不完整邮箱时按钮禁用
    await user.type(screen.getByLabelText(/输入完整邮箱/), 'alpha@icloud')
    expect(screen.getByRole('button', { name: /确认删除/ })).toBeDisabled()
    await user.type(screen.getByLabelText(/输入完整邮箱/), '.com')
    await user.click(screen.getByRole('button', { name: /确认删除/ }))
    await waitFor(() => expect(deletedUrl).toContain('anon_alpha'))
  })

  it('操作失败保留列表并显示错误', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({ success: true, data: { account_id: 'acc_1', count: 2, aliases } }),
      ),
      http.post('/api/aliases/:id/deactivate', () =>
        HttpResponse.json(
          { success: false, code: 'UPSTREAM_FAILURE', message: '停用失败' },
          { status: 502 },
        ),
      ),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()
    await user.click(screen.getAllByRole('button', { name: /停用/ })[0])
    await user.click(screen.getByRole('button', { name: /确认停用/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('停用失败')
    expect(screen.getByText('alpha@icloud.com')).toBeInTheDocument()
  })

  it('展示本地分组与备注,编辑时邮箱只读、可改分组', async () => {
    let putBody: Record<string, unknown> | null = null
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/groups', () =>
        HttpResponse.json({
          success: true,
          data: {
            groups: [
              { id: 'g_shop', name: '购物', color: 'amber', created_at: '', alias_count: 1 },
            ],
            count: 1,
          },
        }),
      ),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            count: 2,
            aliases,
            meta: {
              'alpha@icloud.com': { email: 'alpha@icloud.com', group_id: 'g_shop', note: '注册试用' },
            },
          },
        }),
      ),
      http.put('/api/alias-meta', async ({ request }) => {
        putBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({
          success: true,
          data: { email: 'alpha@icloud.com', group_id: '', note: '' },
        })
      }),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    // 分组列显示归属,备注显示在邮箱下方(分组名同时出现在筛选下拉里,故限定在表格内)
    await waitFor(() =>
      expect(within(screen.getByRole('table')).getByText('购物')).toBeInTheDocument(),
    )
    expect(screen.getByText('注册试用')).toBeInTheDocument()

    const user = userEvent.setup()
    const alphaRow = within(screen.getByRole('table')).getByRole('row', { name: /alpha@icloud\.com/ })
    await user.click(within(alphaRow).getByRole('button', { name: /编辑/ }))

    const dialog = screen.getByRole('dialog')
    const emailInput = within(dialog).getByLabelText('邮箱') as HTMLInputElement
    expect(emailInput.value).toBe('alpha@icloud.com')
    expect(emailInput).toBeDisabled()
    expect(within(dialog).getByLabelText('分组')).toHaveValue('g_shop')

    await user.selectOptions(within(dialog).getByLabelText('分组'), '')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    await waitFor(() =>
      expect(putBody).toMatchObject({
        email: 'alpha@icloud.com',
        account_id: 'acc_1',
        group_id: '',
      }),
    )
  })

  it('按分组筛选:未分组与指定分组', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/groups', () =>
        HttpResponse.json({
          success: true,
          data: {
            groups: [
              { id: 'g_shop', name: '购物', color: 'amber', created_at: '', alias_count: 1 },
            ],
            count: 1,
          },
        }),
      ),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            count: 2,
            aliases,
            meta: {
              'alpha@icloud.com': { email: 'alpha@icloud.com', group_id: 'g_shop' },
            },
          },
        }),
      ),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()

    await user.selectOptions(screen.getByLabelText('分组'), 'g_shop')
    expect(screen.getByText('alpha@icloud.com')).toBeInTheDocument()
    expect(screen.queryByText('beta@icloud.com')).toBeNull()

    await user.selectOptions(screen.getByLabelText('分组'), '__none__')
    expect(await screen.findByText('beta@icloud.com')).toBeInTheDocument()
    expect(screen.queryByText('alpha@icloud.com')).toBeNull()
  })
})

/** 批量接口的请求体形状(测试断言用) */
interface BulkPost {
  account_id: string
  action: string
  group_id: string
  aliases: { anonymous_id: string; email?: string }[]
}

describe('AliasesPage 批量操作', () => {
  beforeEach(() => {
    setCSRFToken('csrf-test')
    server.resetHandlers()
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: { account_id: 'acc_1', count: 2, aliases },
        }),
      ),
      http.get('/api/groups', () =>
        HttpResponse.json({
          success: true,
          data: {
            groups: [
              {
                id: 'g_shop',
                name: '购物',
                color: 'amber',
                created_at: '2026-07-01T00:00:00+08:00',
                alias_count: 0,
              },
            ],
            count: 1,
          },
        }),
      ),
    )
  })

  it('全选当前页后只发一次请求批量停用', async () => {
    let received: BulkPost | null = null
    server.use(
      http.post('/api/alias-bulk', async ({ request }) => {
        received = (await request.json()) as BulkPost
        return HttpResponse.json({
          success: true,
          data: {
            action: 'deactivate',
            total: 2,
            applied: 2,
            succeeded: 2,
            failed: 0,
            results: [
              { anonymous_id: 'anon_alpha', ok: true },
              { anonymous_id: 'anon_beta', ok: true },
            ],
          },
        })
      }),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()

    // 未勾选时不该出现批量条
    expect(screen.queryByRole('region', { name: '批量操作' })).toBeNull()

    await user.click(screen.getByLabelText('全选当前页'))
    const bar = screen.getByRole('region', { name: '批量操作' })
    expect(within(bar).getByText(/已选/)).toHaveTextContent('2')

    await user.click(within(bar).getByRole('button', { name: '停用' }))
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '确认停用' }))

    await waitFor(() => expect(received).not.toBeNull())
    expect(received!.action).toBe('deactivate')
    expect(received!.account_id).toBe('acc_1')
    expect(received!.aliases.map((a) => a.anonymous_id)).toEqual(['anon_alpha', 'anon_beta'])
    // 删除时带上邮箱,便于服务端清理本地分组
    expect(received!.aliases.map((a) => a.email)).toEqual(['alpha@icloud.com', 'beta@icloud.com'])

    expect(await screen.findByText('已停用 2 个邮箱')).toBeInTheDocument()
    // 成功后清空选择,批量条消失
    await waitFor(() => expect(screen.queryByRole('region', { name: '批量操作' })).toBeNull())
  })

  it('部分失败时回显失败的邮箱与原因', async () => {
    server.use(
      http.post('/api/alias-bulk', () =>
        HttpResponse.json({
          success: true,
          data: {
            action: 'delete',
            total: 2,
            applied: 1,
            succeeded: 1,
            failed: 1,
            results: [
              { anonymous_id: 'anon_alpha', email: 'alpha@icloud.com', ok: true },
              {
                anonymous_id: 'anon_beta',
                email: 'beta@icloud.com',
                ok: false,
                error: '上游返回 500',
              },
            ],
          },
        }),
      ),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()

    await user.click(screen.getByLabelText('全选当前页'))
    const bar = screen.getByRole('region', { name: '批量操作' })
    await user.click(within(bar).getByRole('button', { name: /删除/ }))
    // 批量删除不需要输入邮箱,直接确认
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '确认删除' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('1 个邮箱处理失败')
    expect(alert).toHaveTextContent('beta@icloud.com')
    expect(alert).toHaveTextContent('上游返回 500')

    // 失败项保持勾选,方便直接重试
    const stillSelected = screen.getByRole('region', { name: '批量操作' })
    expect(within(stillSelected).getByText(/已选/)).toHaveTextContent('1')
  })

  it('批量归入分组:一次请求带 group_id', async () => {
    let received: BulkPost | null = null
    server.use(
      http.post('/api/alias-bulk', async ({ request }) => {
        received = (await request.json()) as BulkPost
        return HttpResponse.json({
          success: true,
          data: {
            action: 'assign_group',
            group_id: 'g_shop',
            total: 2,
            applied: 2,
            succeeded: 2,
            failed: 0,
            results: [],
          },
        })
      }),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()

    await user.click(screen.getByLabelText('全选当前页'))
    await user.selectOptions(screen.getByLabelText('批量归入分组'), 'g_shop')

    await waitFor(() => expect(received).not.toBeNull())
    expect(received!.action).toBe('assign_group')
    expect(received!.group_id).toBe('g_shop')
    expect(received!.aliases).toHaveLength(2)
    expect(await screen.findByText('已归入分组 2 个邮箱')).toBeInTheDocument()
  })

  it('导出 CSV:按当前筛选结果生成文件', async () => {
    const createObjectURL = vi.fn(() => 'blob:mock')
    const revokeObjectURL = vi.fn()
    Object.assign(URL, { createObjectURL, revokeObjectURL })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})

    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /导出 CSV/ }))

    expect(createObjectURL).toHaveBeenCalledTimes(1)
    expect(click).toHaveBeenCalledTimes(1)
    expect(await screen.findByText('已导出 2 条')).toBeInTheDocument()

    click.mockRestore()
  })
})

describe('AliasesPage 收信活动', () => {
  const DAY = 24 * 60 * 60 * 1000
  const receivedAgo = (days: number) => new Date(Date.now() - days * DAY).toISOString()

  function mockList(meta: Record<string, unknown>) {
    return [
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: { account_id: 'acc_1', count: aliases.length, aliases, meta },
        }),
      ),
    ]
  }

  beforeEach(() => {
    setCSRFToken('csrf-test')
    server.resetHandlers()
  })

  it('展示最近收信相对时间,并可按「从未收信」筛选', async () => {
    server.use(
      ...mockList({
        'alpha@icloud.com': { email: 'alpha@icloud.com', last_received_at: receivedAgo(3) },
      }),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')

    const table = screen.getByRole('table')
    expect(within(table).getByText('3 天前')).toBeInTheDocument()
    // beta 没有任何记录:显示"未记录"而不是"从未收信"
    expect(within(table).getByText('未记录')).toBeInTheDocument()

    const user = userEvent.setup()
    await user.selectOptions(screen.getByLabelText('收信'), 'never')
    const filtered = screen.getByRole('table')
    expect(within(filtered).queryByText('alpha@icloud.com')).toBeNull()
    expect(within(filtered).getByText('beta@icloud.com')).toBeInTheDocument()
  })

  it('90 天未收信:头部给出数量,筛选只留久未收信的号', async () => {
    server.use(
      ...mockList({
        'alpha@icloud.com': { email: 'alpha@icloud.com', last_received_at: receivedAgo(120) },
        'beta@icloud.com': { email: 'beta@icloud.com', last_received_at: receivedAgo(5) },
      }),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    expect(screen.getByText(/90 天未收信 1 个/)).toBeInTheDocument()

    const user = userEvent.setup()
    await user.selectOptions(screen.getByLabelText('收信'), 'stale90')
    const table = screen.getByRole('table')
    expect(within(table).getByText('alpha@icloud.com')).toBeInTheDocument()
    expect(within(table).queryByText('beta@icloud.com')).toBeNull()
  })

  it('刷新收信:发出扫描请求并把结果写回列表', async () => {
    let scanBody: Record<string, unknown> | null = null
    let listCalls = 0
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () => {
        listCalls += 1
        // 第一次没有收信记录,扫描后再拉才带上时间
        const meta =
          listCalls > 1
            ? { 'alpha@icloud.com': { email: 'alpha@icloud.com', last_received_at: receivedAgo(1) } }
            : {}
        return HttpResponse.json({
          success: true,
          data: { account_id: 'acc_1', count: aliases.length, aliases, meta },
        })
      }),
      http.post('/api/alias-activity/scan', async ({ request }) => {
        scanBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            method: 'imap',
            scanned: 42,
            matched: 1,
            updated: 1,
            window_days: 90,
            last_scan_at: new Date().toISOString(),
          },
        })
      }),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    // 两个号都还没有记录
    expect(screen.getAllByText('未记录')).toHaveLength(2)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /刷新收信/ }))

    expect(await screen.findByText('已扫描 42 封邮件，更新 1 个')).toBeInTheDocument()
    expect(scanBody).toEqual({ account_id: 'acc_1' })
    expect(await screen.findByText('昨天')).toBeInTheDocument()
    expect(listCalls).toBeGreaterThan(1)
  })

  it('扫描失败时给出可操作的提示', async () => {
    server.use(
      ...mockList({}),
      http.post('/api/alias-activity/scan', () =>
        HttpResponse.json(
          { success: false, code: 'VALIDATION_ERROR', message: '无可用邮件客户端' },
          { status: 400 },
        ),
      ),
    )
    renderPage()
    await screen.findByText('alpha@icloud.com')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /刷新收信/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('无可用邮件客户端')
  })
})

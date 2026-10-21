import { http, HttpResponse } from 'msw'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import InboxPage from './InboxPage'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'
import { ToastProvider } from '../components/ToastProvider'
import type { AccountSummary, InboxResult } from '../api/types'

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
]

const inboxResult: InboxResult = {
  account_id: 'acc_1',
  alias: 'alpha@icloud.com',
  count: 1,
  method: 'imap',
  messages: [
    {
      id: '1',
      from: 'sender@example.com',
      to: 'alpha@icloud.com',
      subject: '主题一',
      date: '2026-08-04T10:00:00+08:00',
      preview: '预览内容',
    },
  ],
}

function renderPage(initialPath = '/inbox') {
  return render(
    <MemoryRouter initialEntries={[initialPath]}>
      <Routes>
        <Route
          path="/inbox"
          element={
            <ToastProvider>
              <InboxPage />
            </ToastProvider>
          }
        />
      </Routes>
    </MemoryRouter>,
  )
}

describe('InboxPage', () => {
  beforeEach(() => {
    setCSRFToken('csrf-test')
    server.resetHandlers()
  })

  it('账号必选;alias 可空;limit/days 生效;query 经 URLSearchParams', async () => {
    let lastUrl = ''
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', ({ request }) => {
        lastUrl = request.url
        return HttpResponse.json({ success: true, data: inboxResult })
      }),
    )
    renderPage()
    await screen.findByText('主题一')
    // 确认 query 参数
    const url = new URL(lastUrl)
    expect(url.searchParams.get('account_id')).toBe('acc_1')
    expect(url.searchParams.get('limit')).toBe('20')
    expect(url.searchParams.get('days')).toBe('7')
    // 修改 limit/days 再查询
    const user = userEvent.setup()
    await user.selectOptions(screen.getByLabelText(/每页/), '100')
    await user.selectOptions(screen.getByLabelText(/时间范围/), '30')
    await user.click(screen.getByRole('button', { name: /查询/ }))
    await waitFor(() => {
      const u = new URL(lastUrl)
      expect(u.searchParams.get('limit')).toBe('100')
      expect(u.searchParams.get('days')).toBe('30')
    })
  })

  it('从 URL 的 alias 参数初始化筛选,支持别名页直达收件箱', async () => {
    const inboxUrls: string[] = []
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            count: 1,
            aliases: [
              {
                email: 'alpha@icloud.com',
                anonymousId: 'anon_alpha',
                label: 'Alpha',
                active: true,
              },
            ],
          },
        }),
      ),
      http.get('/api/inbox', ({ request }) => {
        inboxUrls.push(request.url)
        return HttpResponse.json({ success: true, data: inboxResult })
      }),
    )
    renderPage('/inbox?account_id=acc_1&alias=alpha%40icloud.com')
    await screen.findByText('主题一')
    await waitFor(() => {
      expect(inboxUrls.some((url) => new URL(url).searchParams.get('alias') === 'alpha@icloud.com')).toBe(true)
    })
    expect(screen.getByLabelText(/别名/)).toHaveValue('alpha@icloud.com')
  })

  it('展示 method=imap 或 web_api', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: { ...inboxResult, method: 'web_api' },
        }),
      ),
    )
    renderPage()
    await screen.findByText('主题一')
    expect(screen.getAllByText(/Web API/).length).toBeGreaterThan(0)
  })

  it('空列表、网络错误、401 状态', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: { account_id: 'acc_1', count: 0, messages: [], method: 'imap' },
        }),
      ),
    )
    renderPage()
    expect(await screen.findByText(/暂无邮件/)).toBeInTheDocument()
  })

  it('恶意 HTML 只作为文本显示,不产生 img 节点', async () => {
    const evil = {
      ...inboxResult,
      messages: [
        {
          id: '2',
          from: 'evil@example.com',
          to: 'alpha@icloud.com',
          subject: '<img src=x onerror=alert(1)>',
          date: '2026-08-04T10:00:00+08:00',
          preview: '<script>alert(2)</script>预览',
        },
      ],
    }
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () => HttpResponse.json({ success: true, data: evil })),
    )
    renderPage()
    await screen.findByText(/<img src=x onerror=alert\(1\)>/)
    expect(document.querySelector('img')).toBeNull()
    expect(document.querySelector('script')).toBeNull()
  })

  it('快速切换筛选:第一请求晚返回不覆盖第二请求', async () => {
    let release: (() => void) | undefined
    let calls = 0
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () => {
        calls++
        if (calls === 1) {
          // 第一次请求挂起,稍后返回旧数据
          return new Promise<Response>((resolve) => {
            release = () =>
              resolve(
                HttpResponse.json({
                  success: true,
                  data: {
                    ...inboxResult,
                    messages: [{ ...inboxResult.messages[0], subject: '旧主题' }],
                  },
                }),
              )
          })
        }
        return HttpResponse.json({
          success: true,
          data: {
            ...inboxResult,
            alias: 'second',
            messages: [
              {
                id: '9',
                from: 's2@example.com',
                to: 'alpha@icloud.com',
                subject: '第二请求主题',
                date: '2026-08-04T11:00:00+08:00',
                preview: '第二请求',
              },
            ],
          },
        })
      }),
    )
    renderPage()
    // 等待筛选区就绪:账号选择器的选项加载完成
    await waitFor(() =>
      expect(screen.getByLabelText(/账号/)).toHaveTextContent('主号'),
    )
    // 触发第二次查询(首次挂起中)。注意:首次请求未返回时没有分页条,
    // 因此这里用「时间范围」而不是底部的「每页」来触发。
    const user = userEvent.setup()
    await user.selectOptions(screen.getByLabelText(/时间范围/), '30')
    await user.click(screen.getByRole('button', { name: /查询/ }))
    await screen.findByText('第二请求主题')
    // 第一次请求此时才返回
    release?.()
    // 旧数据不得覆盖新数据
    await new Promise((r) => setTimeout(r, 100))
    expect(screen.getByText('第二请求主题')).toBeInTheDocument()
    expect(screen.queryByText('旧主题')).toBeNull()
  })

  it('账号选择器默认「全部账号」,并并发查询每个账号后按时间倒序合并', async () => {
    const second: AccountSummary = { ...accounts[0], id: 'acc_2', name: '备用号' }
    const inboxUrls: string[] = []
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: [accounts[0], second] })),
      http.get('/api/aliases', () =>
        HttpResponse.json({ success: true, data: { account_id: 'acc_1', count: 0, aliases: [] } }),
      ),
      http.get('/api/inbox', ({ request }) => {
        const url = new URL(request.url)
        inboxUrls.push(url.toString())
        const id = url.searchParams.get('account_id')
        const isFirst = id === 'acc_1'
        return HttpResponse.json({
          success: true,
          data: {
            account_id: id,
            count: 1,
            method: 'imap',
            messages: [
              {
                id: isFirst ? '1' : '2',
                from: 's@example.com',
                to: 'a@icloud.com',
                subject: isFirst ? '较早的邮件' : '较新的邮件',
                date: isFirst ? '2026-08-01T10:00:00+08:00' : '2026-08-05T10:00:00+08:00',
                preview: 'p',
              },
            ],
          },
        })
      }),
    )
    renderPage()

    // 默认就是「全部账号」
    await waitFor(() => expect(screen.getByLabelText(/账号/)).toHaveValue('__all__'))
    await screen.findByText('较新的邮件')
    // 两个账号都被查询过
    await waitFor(() => {
      const ids = inboxUrls.map((u) => new URL(u).searchParams.get('account_id'))
      expect(ids).toContain('acc_1')
      expect(ids).toContain('acc_2')
    })
    // 合并后按时间倒序:较新的在前
    const rows = screen.getAllByRole('row').slice(1)
    expect(rows[0]).toHaveTextContent('较新的邮件')
    expect(rows[1]).toHaveTextContent('较早的邮件')
  })

  it('「全部账号」下禁用「待转发」', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({ success: true, data: { account_id: 'acc_1', count: 0, aliases: [] } }),
      ),
      http.get('/api/inbox', () =>
        HttpResponse.json({ success: true, data: { account_id: 'acc_1', count: 0, messages: [], method: 'imap' } }),
      ),
    )
    renderPage()
    await waitFor(() => expect(screen.getByLabelText(/账号/)).toHaveValue('__all__'))
    expect(screen.getByRole('button', { name: /待转发/ })).toBeDisabled()
  })

  it('空 subject 显示(无主题)', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: {
            ...inboxResult,
            messages: [{ ...inboxResult.messages[0], subject: '' }],
          },
        }),
      ),
    )
    renderPage()
    expect(await screen.findByText(/（无主题）/)).toBeInTheDocument()
  })

  it('空摘要显示占位符', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: {
            ...inboxResult,
            messages: [{ ...inboxResult.messages[0], preview: '' }],
          },
        }),
      ),
    )
    renderPage()
    expect(await screen.findByText('—')).toBeInTheDocument()
  })

  it('待转发视图需先选别名,选中后按别名查询并展示待转发数量', async () => {
    const forwardUrls: string[] = []
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            count: 1,
            aliases: [{ email: 'alpha@icloud.com', anonymousId: 'anon_alpha', label: 'Alpha', active: true }],
          },
        }),
      ),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: { account_id: 'acc_1', count: 0, messages: [], method: 'imap' },
        }),
      ),
      http.get('/api/forward', ({ request }) => {
        forwardUrls.push(request.url)
        return HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            alias: 'alpha@icloud.com',
            count: 1,
            method: 'imap',
            messages: [
              {
                id: '77',
                from: 'noreply@example.com',
                to: 'alpha@icloud.com',
                subject: '待转发主题',
                date: '2026-08-04T10:00:00+08:00',
                preview: '验证码 123456',
                forwarded: false,
              },
            ],
          },
        })
      }),
    )
    renderPage('/inbox?account_id=acc_1&alias=alpha%40icloud.com')

    const user = userEvent.setup()
    const toggle = await screen.findByRole('button', { name: /待转发/ })
    await waitFor(() => expect(toggle).toBeEnabled())
    await user.click(toggle)

    expect(await screen.findByText('待转发主题')).toBeInTheDocument()
    expect(screen.getByText(/待转发 1 封/)).toBeInTheDocument()
    expect(forwardUrls.some((url) => new URL(url).searchParams.get('alias') === 'alpha@icloud.com')).toBe(true)
  })

  it('未选别名时「待转发」按钮禁用', async () => {
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: { account_id: 'acc_1', count: 0, messages: [], method: 'imap' },
        }),
      ),
    )
    renderPage('/inbox?account_id=acc_1')
    const toggle = await screen.findByRole('button', { name: /待转发/ })
    expect(toggle).toBeDisabled()
  })

  it('「全部标记为已转发」提交当前待转发邮件的 ID', async () => {
    let ackBody: unknown = null
    server.use(
      http.get('/api/accounts', () => HttpResponse.json({ success: true, data: accounts })),
      http.get('/api/aliases', () =>
        HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            count: 1,
            aliases: [{ email: 'alpha@icloud.com', anonymousId: 'anon_alpha', label: 'Alpha', active: true }],
          },
        }),
      ),
      http.get('/api/inbox', () =>
        HttpResponse.json({
          success: true,
          data: { account_id: 'acc_1', count: 0, messages: [], method: 'imap' },
        }),
      ),
      http.get('/api/forward', () =>
        HttpResponse.json({
          success: true,
          data: {
            account_id: 'acc_1',
            alias: 'alpha@icloud.com',
            count: 2,
            method: 'imap',
            messages: [
              { id: '77', from: 'a@example.com', to: 'alpha@icloud.com', subject: '邮件一', date: '2026-08-04T10:00:00+08:00', preview: 'p1', forwarded: false },
              { id: '78', from: 'b@example.com', to: 'alpha@icloud.com', subject: '邮件二', date: '2026-08-04T11:00:00+08:00', preview: 'p2', forwarded: false },
            ],
          },
        }),
      ),
      http.post('/api/forward/ack', async ({ request }) => {
        ackBody = await request.json()
        return HttpResponse.json({ success: true, data: { updated: 2 } })
      }),
    )
    renderPage('/inbox?account_id=acc_1&alias=alpha%40icloud.com')

    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: /待转发/ }))
    await screen.findByText('邮件一')

    await user.click(screen.getByRole('button', { name: /全部标记为已转发/ }))
    await waitFor(() => expect(ackBody).not.toBeNull())
    expect(ackBody).toMatchObject({
      account_id: 'acc_1',
      alias: 'alpha@icloud.com',
      ids: ['77', '78'],
    })
  })
})

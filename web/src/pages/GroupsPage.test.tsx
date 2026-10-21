import { http, HttpResponse } from 'msw'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import GroupsPage from './GroupsPage'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'
import { ToastProvider } from '../components/ToastProvider'
import type { Group } from '../api/types'

const groups: Group[] = [
  { id: 'g_shop', name: '购物', color: 'amber', note: '只在购物站注册用', created_at: '2026-10-01T10:00:00+08:00', alias_count: 3 },
  { id: 'g_work', name: '工作', color: 'blue', created_at: '2026-10-02T10:00:00+08:00', alias_count: 0 },
]

function renderPage() {
  return render(
    <MemoryRouter>
      <ToastProvider>
        <GroupsPage />
      </ToastProvider>
    </MemoryRouter>,
  )
}

describe('GroupsPage', () => {
  beforeEach(() => {
    setCSRFToken('csrf-test')
    server.resetHandlers()
  })

  it('列出分组并显示数量、备注与查看邮箱链接', async () => {
    server.use(
      http.get('/api/groups', () =>
        HttpResponse.json({ success: true, data: { groups, count: 2 } }),
      ),
    )
    renderPage()
    expect(await screen.findByText('购物')).toBeInTheDocument()
    expect(screen.getByText('工作')).toBeInTheDocument()
    expect(screen.getByText('只在购物站注册用')).toBeInTheDocument()
    expect(screen.getByText('未填写备注')).toBeInTheDocument()

    const card = screen.getByText('购物').closest('article') as HTMLElement
    expect(within(card).getByText('3')).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: /查看邮箱/ })).toHaveAttribute(
      'href',
      '/aliases?group_id=g_shop',
    )
  })

  it('空状态提示新建', async () => {
    server.use(
      http.get('/api/groups', () => HttpResponse.json({ success: true, data: { groups: [] } })),
    )
    renderPage()
    expect(await screen.findByText(/还没有分组/)).toBeInTheDocument()
  })

  it('新建分组提交名称/颜色/备注', async () => {
    let postBody: Record<string, unknown> | null = null
    server.use(
      http.get('/api/groups', () => HttpResponse.json({ success: true, data: { groups: [] } })),
      http.post('/api/groups', async ({ request }) => {
        postBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({
          success: true,
          data: { ...groups[0], id: 'g_new', name: '订阅', alias_count: 0 },
        })
      }),
    )
    renderPage()
    await screen.findByText(/还没有分组/)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /新建分组/ }))
    const dialog = screen.getByRole('dialog')
    await user.type(within(dialog).getByLabelText('分组名'), '订阅')
    await user.selectOptions(within(dialog).getByLabelText('颜色'), 'teal')
    await user.type(within(dialog).getByLabelText(/备注/), '每月扣费的')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(postBody).toMatchObject({ name: '订阅', color: 'teal', note: '每月扣费的' }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('删除分组时提示组内数量并调用 DELETE', async () => {
    let deleted = ''
    server.use(
      http.get('/api/groups', () =>
        HttpResponse.json({ success: true, data: { groups, count: 2 } }),
      ),
      http.delete('/api/groups/:id', ({ params }) => {
        deleted = String(params.id)
        return HttpResponse.json({ success: true, data: { deleted: true, unassigned: 3 } })
      }),
    )
    renderPage()
    await screen.findByText('购物')

    const user = userEvent.setup()
    const card = screen.getByText('购物').closest('article') as HTMLElement
    await user.click(within(card).getByRole('button', { name: /删除/ }))
    expect(screen.getByRole('dialog')).toHaveTextContent('组内 3 个邮箱会变为未分组')
    await user.click(screen.getByRole('button', { name: /确认删除/ }))
    await waitFor(() => expect(deleted).toBe('g_shop'))
  })
})

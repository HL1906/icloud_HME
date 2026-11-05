import { http, HttpResponse } from 'msw'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it } from 'vitest'
import TagsPage from './TagsPage'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'
import { ToastProvider } from '../components/ToastProvider'
import type { Tag } from '../api/types'

const tags: Tag[] = [
  {
    id: 'tag_shop',
    name: '购物',
    kind: 'purpose',
    color: 'amber',
    note: '只在购物站注册',
    created_at: '2026-10-01T10:00:00+08:00',
    alias_count: 3,
  },
  {
    id: 'tag_owner',
    name: '客户甲',
    kind: 'owner',
    color: 'teal',
    created_at: '2026-10-02T10:00:00+08:00',
    alias_count: 0,
  },
]

function renderPage() {
  return render(
    <MemoryRouter>
      <ToastProvider>
        <TagsPage />
      </ToastProvider>
    </MemoryRouter>,
  )
}

describe('TagsPage', () => {
  beforeEach(() => {
    setCSRFToken('csrf-test')
    server.resetHandlers()
  })

  it('按分类分组展示标注,并显示使用数量与筛选链接', async () => {
    server.use(
      http.get('/api/tags', () =>
        HttpResponse.json({ success: true, data: { tags, count: 2 } }),
      ),
    )
    renderPage()
    expect(await screen.findByText('购物')).toBeInTheDocument()
    expect(screen.getByText('客户甲')).toBeInTheDocument()
    // 分区标题
    expect(screen.getByRole('heading', { name: '用途' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '使用者' })).toBeInTheDocument()

    const card = screen.getByText('购物').closest('article') as HTMLElement
    expect(within(card).getByText('3 个邮箱')).toBeInTheDocument()
    expect(within(card).getByText('只在购物站注册')).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: /查看邮箱/ })).toHaveAttribute(
      'href',
      '/aliases?tag_id=tag_shop',
    )
  })

  it('空状态提示新建,并在对应分区里新建', async () => {
    let postBody: Record<string, unknown> | null = null
    server.use(
      http.get('/api/tags', () => HttpResponse.json({ success: true, data: { tags: [] } })),
      http.post('/api/tags', async ({ request }) => {
        postBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({
          success: true,
          data: { ...tags[1], id: 'tag_new', name: '运营组' },
        })
      }),
    )
    renderPage()
    await screen.findByText(/还没有标注/)

    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /新建标注/ }))
    const dialog = screen.getByRole('dialog')
    await user.type(within(dialog).getByLabelText('标注名'), '运营组')
    await user.selectOptions(within(dialog).getByLabelText('分类'), 'owner')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(postBody).toMatchObject({ name: '运营组', kind: 'owner' }),
    )
  })

  it('删除标注时提示会影响多少个邮箱', async () => {
    let deleted = ''
    server.use(
      http.get('/api/tags', () =>
        HttpResponse.json({ success: true, data: { tags, count: 2 } }),
      ),
      http.delete('/api/tags/:id', ({ params }) => {
        deleted = String(params.id)
        return HttpResponse.json({ success: true, data: { deleted: true, affected: 3 } })
      }),
    )
    renderPage()
    await screen.findByText('购物')

    const user = userEvent.setup()
    const card = screen.getByText('购物').closest('article') as HTMLElement
    await user.click(within(card).getByRole('button', { name: /删除/ }))
    expect(screen.getByRole('dialog')).toHaveTextContent('3 个邮箱上的这个标注会被摘掉')
    await user.click(screen.getByRole('button', { name: /确认删除/ }))
    await waitFor(() => expect(deleted).toBe('tag_shop'))
  })
})

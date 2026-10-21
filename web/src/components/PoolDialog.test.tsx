import { http, HttpResponse } from 'msw'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import PoolDialog from './PoolDialog'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'
import type { PoolConfig } from '../api/types'

const existing: PoolConfig = {
  account_id: 'acc_1',
  enabled: true,
  target: 30,
  count_mode: 'all',
  max_per_run: 2,
  interval_sec: 900, // 15 分钟
  group_id: 'g_shop',
  last_check_at: '2026-10-21T10:00:00+08:00',
  last_created: 2,
  total_created: 17,
}

beforeEach(() => {
  setCSRFToken('csrf-test')
})

describe('PoolDialog 自动补号设置', () => {
  it('打开时按现有配置回填,并把分钟换算回秒提交', async () => {
    let putBody: Record<string, unknown> | null = null
    server.use(
      http.get('/api/groups', () =>
        HttpResponse.json({
          success: true,
          data: {
            groups: [
              { id: 'g_shop', name: '购物', color: 'amber', created_at: '', alias_count: 3 },
              { id: 'g_work', name: '工作', color: 'blue', created_at: '', alias_count: 1 },
            ],
          },
        }),
      ),
      http.put('/api/pool/:id', async ({ request, params }) => {
        putBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({
          success: true,
          data: { account_id: params.id, ...putBody },
        })
      }),
    )

    const onSaved = vi.fn()
    const user = userEvent.setup()
    render(
      <PoolDialog
        accountId="acc_1"
        accountName="主号"
        config={existing}
        open
        onClose={() => {}}
        onSaved={onSaved}
      />,
    )

    // 回填:目标 30、口径「算全部」、间隔 15 分钟
    expect((screen.getByLabelText('目标数量') as HTMLInputElement).value).toBe('30')
    expect((screen.getByLabelText('统计口径') as HTMLSelectElement).value).toBe('all')
    expect((screen.getByLabelText('检查间隔（分钟）') as HTMLInputElement).value).toBe('15')
    // 分组异步拉取,等选项出现后再断言回填
    await waitFor(() =>
      expect((screen.getByLabelText('自动归入分组') as HTMLSelectElement).value).toBe('g_shop'),
    )

    const targetInput = screen.getByLabelText('目标数量')
    await user.clear(targetInput)
    await user.type(targetInput, '50')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => expect(onSaved).toHaveBeenCalled())
    expect(putBody).toMatchObject({
      enabled: true,
      target: 50,
      count_mode: 'all',
      max_per_run: 2,
      interval_sec: 900,
      group_id: 'g_shop',
    })
  })

  it('目标数量超范围时不发请求', async () => {
    let called = false
    server.use(
      http.put('/api/pool/:id', () => {
        called = true
        return HttpResponse.json({ success: true, data: {} })
      }),
    )

    const user = userEvent.setup()
    render(
      <PoolDialog
        accountId="acc_1"
        accountName="主号"
        config={null}
        open
        onClose={() => {}}
        onSaved={() => {}}
      />,
    )

    const targetInput = screen.getByLabelText('目标数量')
    await user.clear(targetInput)
    await user.type(targetInput, '999')
    await user.click(screen.getByRole('button', { name: '保存' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('目标数量需在 1–200 之间')
    expect(called).toBe(false)
  })

  it('「立即补齐一次」调用 run 接口', async () => {
    let runCalled = false
    server.use(
      http.post('/api/pool/:id/run', ({ params }) => {
        runCalled = true
        return HttpResponse.json({
          success: true,
          data: { account_id: params.id, triggered: true },
        })
      }),
    )

    const onSaved = vi.fn()
    const user = userEvent.setup()
    render(
      <PoolDialog
        accountId="acc_1"
        accountName="主号"
        config={existing}
        open
        onClose={() => {}}
        onSaved={onSaved}
      />,
    )

    await user.click(screen.getByRole('button', { name: '立即补齐一次' }))
    await waitFor(() => expect(runCalled).toBe(true))
    expect(onSaved).toHaveBeenCalledWith('已触发一次补齐，稍后查看结果')
  })

  it('「停止自动补号」调用 DELETE', async () => {
    let deleteCalled = false
    server.use(
      http.delete('/api/pool/:id', () => {
        deleteCalled = true
        return HttpResponse.json({ success: true, data: {} })
      }),
    )

    const onSaved = vi.fn()
    const user = userEvent.setup()
    render(
      <PoolDialog
        accountId="acc_1"
        accountName="主号"
        config={existing}
        open
        onClose={() => {}}
        onSaved={onSaved}
      />,
    )

    await user.click(screen.getByRole('button', { name: '停止自动补号' }))
    await waitFor(() => expect(deleteCalled).toBe(true))
    expect(onSaved).toHaveBeenCalledWith('已停止该账号的自动补号')
  })
})

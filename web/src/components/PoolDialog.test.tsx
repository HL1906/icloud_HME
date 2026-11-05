import { http, HttpResponse } from 'msw'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import PoolDialog from './PoolDialog'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'
import type { PoolConfig, PoolPreview } from '../api/types'

const existing: PoolConfig = {
  account_id: 'acc_1',
  enabled: true,
  target: 20,
  max_per_run: 2,
  interval_sec: 900, // 15 分钟
  group_id: 'g_shop',
  filter: { group_id: '', state: 'stock', active: 'active' },
  last_check_at: '2026-10-21T10:00:00+08:00',
  last_current: 7,
  last_created: 2,
  total_created: 17,
}

const preview: PoolPreview = {
  account_id: 'acc_1',
  current: 7,
  target: 20,
  to_create: 2,
  breakdown: { stock: 7, issued: 12, revoked: 1, inactive: 2, total: 20 },
}

/** 默认把试算接口挂上,避免每个用例都写一遍 */
function mockPreview(data: PoolPreview = preview) {
  server.use(
    http.get('/api/pool/:id/preview', () => HttpResponse.json({ success: true, data })),
  )
}

function mockGroups() {
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
  )
}

beforeEach(() => {
  setCSRFToken('csrf-test')
})

describe('PoolDialog 自动补号设置', () => {
  it('打开时按现有配置回填,并把分钟换算回秒提交', async () => {
    let putBody: Record<string, unknown> | null = null
    mockGroups()
    mockPreview()
    server.use(
      http.put('/api/pool/:id', async ({ request, params }) => {
        putBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ success: true, data: { account_id: params.id } })
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

    // 回填:库存下限 20、只数库存、仅启用中、间隔 15 分钟
    expect((screen.getByLabelText('可用库存不低于') as HTMLInputElement).value).toBe('20')
    // 口径固定:只数库存,已分发/已回收/已停用都不计入
    expect(screen.getByText(/只数还没发出去的库存/)).toBeInTheDocument()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    expect(
      (screen.getByRole('checkbox', { name: /已停用的不算库存/ }) as HTMLInputElement).checked,
    ).toBe(true)
    expect((screen.getByLabelText('检查间隔') as HTMLInputElement).value).toBe('15')
    await waitFor(() =>
      expect((screen.getByLabelText('新号归入') as HTMLSelectElement).value).toBe('g_shop'),
    )

    const targetInput = screen.getByLabelText('可用库存不低于')
    await user.clear(targetInput)
    await user.type(targetInput, '50')
    await user.click(screen.getByRole('button', { name: '保存' }))

    await waitFor(() => expect(onSaved).toHaveBeenCalled())
    expect(putBody).toMatchObject({
      enabled: true,
      target: 50,
      max_per_run: 2,
      interval_sec: 900,
      group_id: 'g_shop',
      filter: { group_id: '', state: 'stock', active: 'active' },
    })
  })

  it('试算结果说明当前库存和本轮会补几个', async () => {
    mockGroups()
    mockPreview()
    render(
      <PoolDialog
        accountId="acc_1"
        accountName="主号"
        config={existing}
        open
        onClose={() => {}}
        onSaved={() => {}}
      />,
    )

    expect(await screen.findByText(/本轮会补/)).toBeInTheDocument()
    expect(screen.getByText('2')).toBeInTheDocument()
    // 状态分布也一并展示,便于判断"还剩多少库存"
    expect(screen.getByText(/库存 7 · 已分发 12 · 已回收 1/)).toBeInTheDocument()
  })

  it('库存达标时不提示补号', async () => {
    mockGroups()
    mockPreview({ ...preview, current: 25, to_create: 0 })
    render(
      <PoolDialog
        accountId="acc_1"
        accountName="主号"
        config={existing}
        open
        onClose={() => {}}
        onSaved={() => {}}
      />,
    )

    expect(await screen.findByText(/已达到 20 个，不用补/)).toBeInTheDocument()
  })

  it('按分组统计时,新号强制归入同一分组', async () => {
    let putBody: Record<string, unknown> | null = null
    mockGroups()
    mockPreview()
    server.use(
      http.put('/api/pool/:id', async ({ request, params }) => {
        putBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json({ success: true, data: { account_id: params.id } })
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

    // 分组选项是异步拉的,等它出现(两个下拉都有分组选项,这里限定到统计范围那个)
    await waitFor(() =>
      expect(
        within(screen.getByLabelText('限定分组')).getByRole('option', { name: '购物' }),
      ).toBeInTheDocument(),
    )
    await user.selectOptions(screen.getByLabelText('限定分组'), 'g_shop')
    // 归入分组被锁定并跟随统计范围
    expect((screen.getByLabelText('新号归入') as HTMLSelectElement).disabled).toBe(true)
    expect((screen.getByLabelText('新号归入') as HTMLSelectElement).value).toBe('g_shop')

    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(putBody).not.toBeNull())
    expect(putBody).toMatchObject({
      group_id: 'g_shop',
      filter: { group_id: 'g_shop', state: 'stock', active: 'active' },
    })
  })

  it('库存下限超范围时不发请求', async () => {
    let called = false
    mockPreview()
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

    const targetInput = screen.getByLabelText('可用库存不低于')
    await user.clear(targetInput)
    await user.type(targetInput, '999')
    await user.click(screen.getByRole('button', { name: '保存' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('库存下限需在 1–200 之间')
    expect(called).toBe(false)
  })

  it('「立即补一次」调用 run 接口', async () => {
    let runCalled = false
    mockPreview()
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

    await user.click(screen.getByRole('button', { name: '立即补一次' }))
    await waitFor(() => expect(runCalled).toBe(true))
    expect(onSaved).toHaveBeenCalledWith('已触发一次补齐，稍后查看结果')
  })

  it('「停止自动补号」调用 DELETE', async () => {
    let deleteCalled = false
    mockPreview()
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

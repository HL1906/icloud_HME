import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import Pagination from './Pagination'
import PageHeader from './PageHeader'

describe('Pagination', () => {
  it('展示区间与总数,首页时禁用上一页', () => {
    render(
      <Pagination page={1} pageSize={10} total={25} onPageChange={() => {}} onPageSizeChange={() => {}} />,
    )
    expect(screen.getByText(/共 25 条（第 1–10 条）/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '下一页' })).toBeEnabled()
  })

  it('末页禁用下一页,页码高亮当前页', () => {
    render(
      <Pagination page={3} pageSize={10} total={25} onPageChange={() => {}} />,
    )
    expect(screen.getByText(/共 25 条（第 21–25 条）/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '第 3 页' })).toHaveAttribute('aria-current', 'page')
  })

  it('点击页码与上一页/下一页触发回调', async () => {
    const onPageChange = vi.fn()
    const user = userEvent.setup()
    render(<Pagination page={2} pageSize={10} total={50} onPageChange={onPageChange} />)

    await user.click(screen.getByRole('button', { name: '第 3 页' }))
    expect(onPageChange).toHaveBeenCalledWith(3)

    await user.click(screen.getByRole('button', { name: '上一页' }))
    expect(onPageChange).toHaveBeenCalledWith(1)

    await user.click(screen.getByRole('button', { name: '下一页' }))
    expect(onPageChange).toHaveBeenCalledWith(3)
  })

  it('页码跳转:回车跳转,越界收敛到有效范围', async () => {
    const onPageChange = vi.fn()
    const user = userEvent.setup()
    render(<Pagination page={1} pageSize={10} total={50} onPageChange={onPageChange} />)

    const input = screen.getByLabelText('跳转到页码')
    await user.type(input, '99{Enter}')
    // 共 5 页,99 应收敛到 5
    expect(onPageChange).toHaveBeenLastCalledWith(5)

    await user.type(input, '2{Enter}')
    expect(onPageChange).toHaveBeenLastCalledWith(2)
  })

  it('每页条数变化触发回调', async () => {
    const onPageSizeChange = vi.fn()
    const user = userEvent.setup()
    render(
      <Pagination page={1} pageSize={10} total={100} onPageChange={() => {}} onPageSizeChange={onPageSizeChange} />,
    )
    await user.selectOptions(screen.getByLabelText('每页条数'), '50')
    expect(onPageSizeChange).toHaveBeenCalledWith(50)
  })

  it('总数为 0 时不渲染', () => {
    const { container } = render(
      <Pagination page={1} pageSize={10} total={0} onPageChange={() => {}} />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('数据变少导致当前页越界时,回落到最后一页', () => {
    // page=9 但只有 2 页数据
    render(<Pagination page={9} pageSize={10} total={15} onPageChange={() => {}} />)
    expect(screen.getByText(/共 15 条（第 11–15 条）/)).toBeInTheDocument()
  })
})

describe('PageHeader', () => {
  it('只渲染标题,不出现面包屑与说明小字', () => {
    render(<PageHeader title="隐藏邮箱" />)
    expect(screen.getByRole('heading', { level: 2, name: '隐藏邮箱' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '面包屑' })).toBeNull()
  })

  it('渲染右侧操作区', () => {
    render(<PageHeader title="账号管理" actions={<button type="button">添加账号</button>} />)
    expect(screen.getByRole('button', { name: '添加账号' })).toBeInTheDocument()
  })
})

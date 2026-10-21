import { useState, type ReactNode } from 'react'

interface PaginationProps {
  /** 当前页码(从 1 开始) */
  page: number
  /** 每页条数 */
  pageSize: number
  /** 总条数 */
  total: number
  onPageChange: (page: number) => void
  onPageSizeChange?: (size: number) => void
  /** 可选:右侧补充说明 */
  extra?: ReactNode
  /** 数据为空时也渲染(保留每页条数与页码位置);默认隐藏 */
  showWhenEmpty?: boolean
}

const PAGE_SIZES = [10, 20, 50, 100]

/**
 * 计算页码序列。
 * 页数不多(<=9)时全部平铺,便于直接点选;超出时首尾固定 + 当前页附近展开。
 */
function buildPageList(current: number, totalPages: number): (number | 'gap')[] {
  if (totalPages <= 9) {
    return Array.from({ length: totalPages }, (_, i) => i + 1)
  }
  const pages: (number | 'gap')[] = [1]
  const start = Math.max(2, current - 1)
  const end = Math.min(totalPages - 1, current + 1)

  if (start > 2) pages.push('gap')
  for (let p = start; p <= end; p++) pages.push(p)
  if (end < totalPages - 1) pages.push('gap')
  pages.push(totalPages)
  return pages
}

/** 统一分页条:总数 + 页码 + 每页条数。 */
export default function Pagination({
  page,
  pageSize,
  total,
  onPageChange,
  onPageSizeChange,
  extra,
  showWhenEmpty = false,
}: PaginationProps) {
  const totalPages = Math.max(1, Math.ceil(total / pageSize))
  // 数据变少导致当前页越界时,回落到最后一页
  const current = Math.min(page, totalPages)
  const from = total === 0 ? 0 : (current - 1) * pageSize + 1
  const to = Math.min(current * pageSize, total)

  if (total === 0 && !showWhenEmpty) {
    return null
  }

  // 摘要:共 N 条 + 当前区间,总数始终可见
  const summary = total === 0 ? '共 0 条' : `共 ${total} 条（第 ${from}–${to} 条）`

  return (
    <div className="pagination" role="navigation" aria-label="分页">
      <span className="pagination-summary">
        {summary}
        {extra}
      </span>
      <span className="pagination-spacer" />
      {onPageSizeChange && (
        <label className="pagination-size">
          每页
          <select
            value={pageSize}
            onChange={(e) => onPageSizeChange(Number(e.target.value))}
            aria-label="每页条数"
          >
            {PAGE_SIZES.map((size) => (
              <option key={size} value={size}>
                {size}
              </option>
            ))}
          </select>
          条，共 {totalPages} 页
        </label>
      )}
      {/* 空数据时页码与跳转没有意义,只保留每页条数 */}
      {total > 0 && (
        <div className="pagination-pages">
          <button
            type="button"
            disabled={current <= 1}
            onClick={() => onPageChange(current - 1)}
            aria-label="上一页"
          >
            上一页
          </button>
          {buildPageList(current, totalPages).map((item, index) =>
            item === 'gap' ? (
              <span key={`gap-${index}`} className="pagination-gap" aria-hidden="true">
                …
              </span>
            ) : (
              <button
                key={item}
                type="button"
                className={item === current ? 'active' : undefined}
                aria-label={`第 ${item} 页`}
                aria-current={item === current ? 'page' : undefined}
                onClick={() => onPageChange(item)}
              >
                {item}
              </button>
            ),
          )}
          <button
            type="button"
            disabled={current >= totalPages}
            onClick={() => onPageChange(current + 1)}
            aria-label="下一页"
          >
            下一页
          </button>
          <JumpToPage current={current} totalPages={totalPages} onJump={onPageChange} />
        </div>
      )}
    </div>
  )
}

/** 页码直达:输入页码回车/失焦后跳转,越界自动收敛。 */
function JumpToPage({
  current,
  totalPages,
  onJump,
}: {
  current: number
  totalPages: number
  onJump: (page: number) => void
}) {
  const [value, setValue] = useState('')

  function commit() {
    const parsed = Number(value)
    setValue('')
    if (!Number.isFinite(parsed) || parsed < 1) return
    // 越界收敛到有效范围,避免跳到空白页
    onJump(Math.min(Math.floor(parsed), totalPages))
  }

  return (
    <label className="pagination-jump">
      跳至
      <input
        type="number"
        min={1}
        max={totalPages}
        value={value}
        placeholder={String(current)}
        onChange={(e) => setValue(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            commit()
          }
        }}
        onBlur={commit}
        aria-label="跳转到页码"
      />
      页
    </label>
  )
}

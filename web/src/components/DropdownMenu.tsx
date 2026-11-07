import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
} from 'react'
import { createPortal } from 'react-dom'

interface DropdownMenuProps {
  /** 触发按钮的无障碍标签 */
  label: string
  /** 触发按钮上显示的文字 */
  trigger: ReactNode
  /** 菜单内容 */
  children: ReactNode
  /** 菜单对齐方式 */
  align?: 'left' | 'right'
}

/**
 * 浮动下拉菜单。
 *
 * 关键点:菜单通过 portal 渲染到 document.body,
 * 否则会被 `.table-wrap { overflow-x: auto }` 之类的祖先容器裁剪,
 * 表现为点开后在单元格里挤出横向/纵向滚动条。
 *
 * 同时处理:点外部关闭、Esc 关闭、滚动/缩放时跟随或关闭。
 */
export default function DropdownMenu({ label, trigger, children, align = 'right' }: DropdownMenuProps) {
  const [open, setOpen] = useState(false)
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  const close = useCallback(() => setOpen(false), [])

  // 打开后计算位置:默认贴着触发按钮下方,空间不足时往上翻
  useLayoutEffect(() => {
    if (!open || !triggerRef.current) return
    const rect = triggerRef.current.getBoundingClientRect()
    const menuHeight = menuRef.current?.offsetHeight ?? 0
    const menuWidth = menuRef.current?.offsetWidth ?? 0
    const spaceBelow = window.innerHeight - rect.bottom
    const flipUp = menuHeight > 0 && spaceBelow < menuHeight + 8 && rect.top > menuHeight + 8

    const top = flipUp ? rect.top - menuHeight - 4 : rect.bottom + 4
    const left = align === 'right' ? rect.right - menuWidth : rect.left
    setPos({
      top: Math.max(8, top),
      left: Math.max(8, Math.min(left, window.innerWidth - menuWidth - 8)),
    })
  }, [open, align])

  // 点击外部 / 按 Esc 关闭;滚动与缩放时关闭,避免菜单脱离按钮
  useEffect(() => {
    if (!open) return

    function onPointerDown(e: MouseEvent) {
      const target = e.target as Node
      if (menuRef.current?.contains(target) || triggerRef.current?.contains(target)) return
      close()
    }
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        close()
        triggerRef.current?.focus()
      }
    }
    function onScroll() {
      close()
    }

    document.addEventListener('mousedown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', onScroll)

    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
      window.removeEventListener('scroll', onScroll, true)
      window.removeEventListener('resize', onScroll)
    }
  }, [open, close])

  // 点击菜单内的操作项后自动关闭。
  //
  // 必须用 React 的冒泡阶段处理:子项 onClick 先执行,事件冒到容器再关闭。
  // 早期版本用原生 addEventListener('click') 挂在菜单容器上,它在 React
  // 合成事件派发之前触发,setOpen(false) 会同步卸载菜单(React 对"非 React
  // 事件里触发的更新"会同步刷新),等事件冒泡到 body 时按钮已被卸载,
  // 子项的 onClick 永远收不到 —— 表现为点菜单项完全没反应。
  const handleMenuClick = (e: ReactMouseEvent<HTMLDivElement>) => {
    if ((e.target as HTMLElement).closest('button, a')) close()
  }

  // 键盘可达性:子项(按钮/链接)的 Enter/Space 由浏览器转成原生 click
  // 冒泡到容器,由上面的 onClick 统一关闭 —— 所以这里绝不能替子项关闭
  // (keydown 阶段关闭会卸载菜单,keyup 后的 click 事件就丢了,子项动作失效)。
  // 只处理直接落在容器自身上的按键(容器 tabIndex=-1,仅兜底防止 Space 滚动页面)。
  const handleMenuKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.target === e.currentTarget && (e.key === 'Enter' || e.key === ' ')) {
      e.preventDefault()
    }
  }

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        className="more-menu-trigger"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        {trigger}
      </button>
      {open &&
        createPortal(
          <div
            ref={menuRef}
            className="dropdown-menu"
            role="menu"
            tabIndex={-1}
            onClick={handleMenuClick}
            onKeyDown={handleMenuKeyDown}
            style={pos ? { top: pos.top, left: pos.left } : { top: -9999, left: -9999 }}
          >
            {children}
          </div>,
          document.body,
        )}
    </>
  )
}

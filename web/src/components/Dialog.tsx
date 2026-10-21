import { useEffect, useRef, type ReactNode } from 'react'

interface DialogProps {
  title: string
  open: boolean
  onClose: () => void
  children: ReactNode
}

/** 可访问 Dialog:Escape 关闭、焦点圈定、关闭后回到触发按钮 */
export default function Dialog({ title, open, onClose, children }: DialogProps) {
  const ref = useRef<HTMLDivElement>(null)
  const lastFocused = useRef<Element | null>(null)
  // 用 ref 保存最新的 onClose:避免调用方每次渲染传入新函数时,
  // 下面的 effect 被反复重建(重建会重置焦点,打断用户输入)
  const onCloseRef = useRef(onClose)

  useEffect(() => {
    onCloseRef.current = onClose
  }, [onClose])

  useEffect(() => {
    if (!open) return
    lastFocused.current = document.activeElement
    const node = ref.current
    if (!node) return
    const focusables = () =>
      Array.from(
        node.querySelectorAll<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
        ),
      ).filter((el) => !el.hasAttribute('disabled'))
    // 初始焦点优先落到表单控件,其次才是关闭按钮,避免一打开就聚焦"关闭"
    const initial =
      focusables().find((el) => el.tagName === 'INPUT' || el.tagName === 'SELECT' || el.tagName === 'TEXTAREA') ??
      focusables()[0]
    initial?.focus()

    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        onCloseRef.current()
        return
      }
      if (e.key !== 'Tab') return
      const items = focusables()
      if (items.length === 0) return
      const first = items[0]
      const last = items[items.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('keydown', handleKey)
      if (lastFocused.current instanceof HTMLElement) {
        lastFocused.current.focus()
      }
    }
  }, [open])

  if (!open) return null

  function handleBackdropActivate() {
    onClose()
  }

  return (
    <div className="dialog-backdrop">
      <button
        type="button"
        className="dialog-backdrop-close"
        aria-label="关闭对话框"
        tabIndex={-1}
        onClick={handleBackdropActivate}
      />
      <div
        ref={ref}
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-label={title}
      >
        <button
          type="button"
          className="dialog-close"
          aria-label="关闭对话框"
          onClick={onClose}
        >
          &times;
        </button>
        <h3>{title}</h3>
        {children}
      </div>
    </div>
  )
}

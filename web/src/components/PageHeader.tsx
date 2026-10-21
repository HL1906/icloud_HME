import type { ReactNode } from 'react'

interface PageHeaderProps {
  title: string
  /** 右侧操作区 */
  actions?: ReactNode
}

/**
 * 统一页头:仅标题 + 右侧操作。
 * 不渲染面包屑与说明小字,保持界面干净。
 */
export default function PageHeader({ title, actions }: PageHeaderProps) {
  return (
    <header className="page-header">
      <h2 className="page-title">{title}</h2>
      {actions && <div className="page-actions">{actions}</div>}
    </header>
  )
}

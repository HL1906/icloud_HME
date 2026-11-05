import type { Tag } from '../api/types'

interface TagChipProps {
  tag: Tag
  /** 传入后右侧显示 × 按钮（用于从邮箱上摘掉标注） */
  onRemove?: () => void
  /** 传入后整块可点击（用于点开筛选） */
  onClick?: () => void
  /** 紧凑模式：列表单元格里用 */
  small?: boolean
}

/** 标注小药丸。颜色与分组共用一套 data-color。 */
export default function TagChip({ tag, onRemove, onClick, small }: TagChipProps) {
  const className = `tag-chip${small ? ' tag-chip-sm' : ''}${onClick ? ' tag-chip-clickable' : ''}`
  const content = (
    <>
      <span className="tag-chip-dot" data-color={tag.color} aria-hidden="true" />
      <span className="tag-chip-name">{tag.name}</span>
      {onRemove && (
        <button
          type="button"
          className="tag-chip-remove"
          aria-label={`移除标注 ${tag.name}`}
          onClick={(e) => {
            e.stopPropagation()
            onRemove()
          }}
        >
          ×
        </button>
      )}
    </>
  )
  if (onClick) {
    return (
      <button type="button" className={className} onClick={onClick} title={`按标注「${tag.name}」筛选`}>
        {content}
      </button>
    )
  }
  return (
    <span className={className} title={tag.note || tag.name}>
      {content}
    </span>
  )
}

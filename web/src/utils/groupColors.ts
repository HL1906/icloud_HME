/** 分组颜色与中文名。与 styles.css 的 .group-dot[data-color] 一一对应。 */
export const GROUP_COLORS = ['blue', 'teal', 'green', 'amber', 'rose', 'purple', 'slate'] as const

export type GroupColor = (typeof GROUP_COLORS)[number]

export const COLOR_LABELS: Record<GroupColor, string> = {
  blue: '蓝色',
  teal: '青色',
  green: '绿色',
  amber: '琥珀',
  rose: '玫红',
  purple: '紫色',
  slate: '灰色',
}

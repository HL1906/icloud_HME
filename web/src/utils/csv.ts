/**
 * 极简 CSV 导出。
 *
 * 只做「把二维数组变成 CSV 文本并触发浏览器下载」这一件事，
 * 不引入额外依赖——导出的字段都是纯文本，Excel / Numbers 都能直接打开。
 */

export type CSVCell = string | number | boolean | null | undefined

/**
 * 转义单个单元格。
 *
 * RFC 4180 规定：包含引号、逗号或换行的字段必须整体加双引号，
 * 字段内的双引号写成两个双引号。
 */
function escapeCell(value: CSVCell): string {
  if (value === null || value === undefined) return ''
  const text = String(value)
  if (/[",\r\n]/.test(text)) {
    return `"${text.replace(/"/g, '""')}"`
  }
  return text
}

/** 把二维数组拼成 CSV 文本（CRLF 行尾，兼容 Excel）。 */
export function toCSV(rows: CSVCell[][]): string {
  return rows.map((row) => row.map(escapeCell).join(',')).join('\r\n')
}

/**
 * 生成 CSV 文件并下载。
 *
 * 带 UTF-8 BOM：Excel 打开不带 BOM 的 UTF-8 CSV 时会把中文显示成乱码，
 * 这是 Windows 上最常见的「导出后全是问号」的原因。
 */
export function downloadCSV(filename: string, rows: CSVCell[][]): void {
  const blob = new Blob(['\uFEFF', toCSV(rows)], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  // 立即回收会让部分浏览器中断下载，放到下一轮事件循环再释放
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

/** 生成带时间戳的文件名，避免多次导出互相覆盖。 */
export function timestampedName(prefix: string, extension = 'csv'): string {
  const now = new Date()
  const pad = (value: number) => String(value).padStart(2, '0')
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}`
  return `${prefix}-${stamp}.${extension}`
}

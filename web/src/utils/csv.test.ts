import { describe, expect, it } from 'vitest'
import { toCSV, timestampedName } from './csv'

describe('toCSV', () => {
  it('普通字段直接拼接', () => {
    expect(
      toCSV([
        ['邮箱', '分组'],
        ['a@icloud.com', '购物'],
      ]),
    ).toBe('邮箱,分组\r\na@icloud.com,购物')
  })

  it('含逗号、引号、换行的字段按 RFC 4180 转义', () => {
    expect(toCSV([['a,b', 'say "hi"', 'line1\nline2']])).toBe(
      '"a,b","say ""hi""","line1\nline2"',
    )
  })

  it('空值输出空字符串而不是 null/undefined', () => {
    expect(toCSV([[null, undefined, 0, false]])).toBe(',,0,false')
  })
})

describe('timestampedName', () => {
  it('带日期时间戳与扩展名', () => {
    expect(timestampedName('icloud-邮箱')).toMatch(/^icloud-邮箱-\d{8}-\d{4}\.csv$/)
  })

  it('允许自定义扩展名', () => {
    expect(timestampedName('report', 'txt')).toMatch(/^report-\d{8}-\d{4}\.txt$/)
  })
})

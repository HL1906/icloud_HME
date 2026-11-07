// 测试中的时间断言(如 AppShell 的「登录有效期至 10-23 09:40」)按东八区硬编码,
// 而组件用本地时区格式化时间。固定测试进程时区,保证本地与 CI(UTC 环境)结果一致。
// 必须在任何 Date 格式化发生之前设置,所以放在 setup 文件第一行。
process.env.TZ = 'Asia/Shanghai'

import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterAll, afterEach, beforeAll } from 'vitest'
import { server } from './server'

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => {
  cleanup()
  server.resetHandlers()
})
afterAll(() => server.close())

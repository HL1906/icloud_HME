import { http, HttpResponse } from 'msw'

export const handlers = [
  http.post('/api/auth/login', () =>
    HttpResponse.json(
      { success: false, code: 'INVALID_CREDENTIALS', message: '管理员密码错误' },
      { status: 401 },
    ),
  ),
  http.get('/api/auth/session', () =>
    HttpResponse.json(
      { success: false, code: 'AUTH_REQUIRED', message: '请先登录' },
      { status: 401 },
    ),
  ),
  http.get('/api/accounts', () =>
    HttpResponse.json({ success: true, data: [] }),
  ),
  // 默认没有自动补号配置:页面不应显示相关入口
  http.get('/api/pool', () =>
    HttpResponse.json({ success: true, data: { configs: [] } }),
  ),
  // 默认没有分组:邮箱管理页按「全部分组」展示
  http.get('/api/groups', () =>
    HttpResponse.json({ success: true, data: { groups: [], count: 0 } }),
  ),
]

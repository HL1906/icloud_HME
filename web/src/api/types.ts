/**
 * API 类型定义——与 internal/server 冻结契约保持一致。
 */

/** 统一响应包裹 */
export interface ApiResponse<T> {
  success: boolean
  data?: T
  code?: string
  message?: string
}

/** 账号安全摘要(无秘密字段) */
export interface AccountSummary {
  id: string
  name: string
  real_email: string
  icloud_email: string
  host: string
  /** validating: 添加后正在后台校验会话;pending: 尚未配置凭据 */
  status: 'active' | 'validating' | 'pending' | 'error' | string
  alias_total: number
  alias_active: number
  has_cookies: boolean
  has_app_password: boolean
  has_proxy: boolean
  mailbox?: MailboxSummary
  last_validated: string
  status_message?: string
  created_at: string
}

export interface MailboxSummary {
  provider: string
  email: string
  imap_host: string
  imap_port: number
}

/** HME 别名(iCloud 返回字段风格为 camelCase) */
export interface Alias {
  email: string
  anonymousId: string
  label: string
  active: boolean
  createdAt?: string
}

/**
 * 本系统自有的别名元数据。分组与备注不会同步到 iCloud，
 * 只存在本地 alias_meta.json 里，用于自己归类管理。
 */
export interface AliasMeta {
  email: string
  account_id?: string
  /** 空表示未分组 */
  group_id?: string
  note?: string
  /**
   * 最近一次收到投递到该别名的邮件时间（RFC3339）。
   * 由「刷新收信」扫描收件箱得出，没有记录表示还没扫到过。
   */
  last_received_at?: string
  updated_at?: string
}

/** 某账号最近一次收信扫描的摘要 */
export interface AliasActivity {
  account_id: string
  last_scan_at?: string
  /** 本次扫描的邮件封数 */
  scanned: number
  /** 命中别名的封数 */
  matched: number
  window_days: number
}

/** GET /api/alias-activity 的返回 */
export interface AliasActivityResult {
  account_id: string
  available: boolean
  activity: AliasActivity
}

/** POST /api/alias-activity/scan 的返回 */
export interface ScanActivityResult {
  account_id: string
  method: 'imap' | 'web_api' | 'demo' | string
  scanned: number
  matched: number
  /** 最近收信时间被刷新的别名数量 */
  updated: number
  window_days: number
  last_scan_at: string
}

/** 分组（系统自有，非 iCloud 字段） */
export interface Group {
  id: string
  name: string
  color: string
  note?: string
  created_at: string
  /** 本系统登记的、属于该分组的别名数量 */
  alias_count: number
}

/** GET /api/aliases 的返回：别名 + 本地元数据 */
export interface AliasListResult {
  account_id: string
  count: number
  aliases: Alias[]
  /** key 为小写邮箱；没有本地元数据的别名不会出现在这里 */
  meta?: Record<string, AliasMeta>
}

/** 批量操作的单个目标 */
export interface BulkAliasTarget {
  anonymous_id: string
  /** 删除时带上,便于服务端顺带清理本地分组记录 */
  email?: string
}

/**
 * 批量操作类型。
 * - deactivate / reactivate / delete 落到 iCloud,逐条执行;
 * - assign_group 只改本地元数据（group_id 为空表示移出分组）。
 */
export type BulkAliasAction = 'deactivate' | 'reactivate' | 'delete' | 'assign_group'

/** 批量操作的单条结果 */
export interface BulkAliasItemResult {
  anonymous_id?: string
  email?: string
  ok: boolean
  error?: string
}

/** POST /api/alias-bulk 的返回 */
export interface BulkAliasResult {
  action: BulkAliasAction
  total: number
  applied: number
  succeeded: number
  failed: number
  results: BulkAliasItemResult[]
}

/** 邮件摘要 */
export interface InboxMessage {
  id: string
  from: string
  to: string
  subject: string
  date: string
  preview: string
}

export interface FullMessage extends InboxMessage {
  body: string
  content_type: string
}

/** 收件箱查询结果 */
export interface InboxResult {
  account_id: string
  alias?: string
  count: number
  messages: InboxMessage[]
  method: 'imap' | 'web_api'
}

/** 转发专用邮件(带已转发标记) */
export interface ForwardMessage extends InboxMessage {
  forwarded: boolean
  body?: string
}

/** 转发专用查询结果 */
export interface ForwardResult {
  account_id: string
  alias: string
  count: number
  messages: ForwardMessage[]
  method: 'imap' | 'web_api'
}

/** 登录响应 */
export interface LoginResult {
  csrf_token: string
  expires_at: string
}

/** 别名池自动补号配置(前半为用户配置,后半为运行状态) */
export interface PoolConfig {
  account_id: string
  enabled: boolean
  target: number
  /** active: 只算启用中的别名(默认);all: 算全部 */
  count_mode: 'active' | 'all'
  max_per_run: number
  interval_sec: number
  /** 自动创建的别名归入哪个本地分组（空 = 未分组） */
  group_id?: string
  last_check_at?: string
  last_created_at?: string
  last_created?: number
  last_error?: string
  /** 连续失败后自动暂停,需要重新保存配置解除 */
  paused?: boolean
  total_created: number
}

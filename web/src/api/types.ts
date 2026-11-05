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
 * 别名的分发状态（本系统自有）。
 * - stock  库存：建好还没发出去
 * - issued 已分发：已签发取件令牌，链接可以被外人读取
 * - revoked 已回收：发出去的链接已撤销，不再可读
 */
export type AliasState = 'stock' | 'issued' | 'revoked'

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
  /** 分发状态；没有令牌也没登记过时按库存处理 */
  state?: AliasState
  /** 取件令牌（URL 里 e=邮箱:令牌 的后半段），没有则为空 */
  token?: string
  /** 令牌签发时间 */
  issued_at?: string
  /** 这个邮箱发给了谁（纯本地备注，不会同步到 iCloud） */
  recipient?: string
  /** 挂在这个邮箱上的标注 ID（本系统自有的第二套标签，见 Tag） */
  tag_ids?: string[]
  updated_at?: string
}

/**
 * 标注分类。
 * - purpose 用途：这个邮箱拿来干什么（购物/订阅/注册…）
 * - owner   使用者：这个邮箱归谁用（自己/客户甲/团队…）
 * - other   其它
 */
export type TagKind = 'purpose' | 'owner' | 'other'

/**
 * 标注：本系统自有的"第二套标签"。
 *
 * iCloud 的 label 字段建别名时写入、之后不可修改，而且属于 Apple 那边的数据。
 * "这个号干什么用、谁在用"这类信息会变，所以另建一套可编辑、可复用的标注目录。
 * 一个标注可以被多个邮箱使用，一个邮箱也可以挂多个标注。
 */
export interface Tag {
  id: string
  name: string
  kind: TagKind
  color: string
  note?: string
  created_at: string
  /** 被多少个邮箱使用（仅列表接口返回） */
  alias_count?: number
}

/** 设置：运行时可改的部分（改完立即生效，不需要重启） */
export interface AppSettings {
  /** 自动回收总开关 */
  auto_revoke_enabled: boolean
  /** 后台扫描间隔（分钟，1-1440） */
  auto_revoke_interval_min: number
  /** 取件页默认看多少小时（1-168） */
  public_mail_hours: number
  /** 取件页默认看几封（1-50） */
  public_mail_limit: number
  /** 分发链接的站点前缀；留空表示按访问地址自动推导 */
  public_base_url: string
}

/** 设置页要展示的只读部署信息 */
export interface SettingsEnv {
  data_dir: string
  /** 环境变量里的对外域名；非空时会被部署配置覆盖 */
  public_base_url_env: string
  version: string
  secure_cookie: boolean
}

/** GET /api/settings 的返回 */
export interface SettingsResult {
  settings: AppSettings
  env: SettingsEnv
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
  method: 'imap' | 'web_api' | string
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
  /**
   * 该分组下取件链接的自动回收天数：签发 N 天后自动撤销。
   * 0 或缺省表示不自动回收。只对已归入本分组的别名生效。
   */
  revoke_after_days?: number
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
 * - assign_group 只改本地元数据（group_id 为空表示移出分组）;
 * - issue_token / revoke_token 只动本地取件令牌,同样不碰 iCloud。
 */
export type BulkAliasAction =
  | 'deactivate'
  | 'reactivate'
  | 'delete'
  | 'assign_group'
  | 'issue_token'
  | 'revoke_token'

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
  /** 仅 issue_token：邮箱 -> 取件链接，前端直接导出成分发清单 */
  links?: Record<string, string>
}

/** POST /api/alias-token 的返回 */
export interface AliasTokenResult {
  email: string
  account_id: string
  token: string
  state: AliasState
  issued_at?: string
  recipient?: string
  /** 取件页（给人点开） */
  url: string
  /** 同一链接的 JSON 形态（给脚本对接） */
  url_json: string
  /** wdmail 风格分发串：邮箱----链接 */
  distribute: string
  public_base: string
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
/** 补号的统计范围：目标数量针对的是筛选后的别名，而不是账号下全部别名 */
export interface PoolFilter {
  /** 限定分组（空 = 不限）。选了分组时，新补的邮箱也会归入这个分组 */
  group_id?: string
  /** 分发状态：all（不限）| stock（只算库存，即还没发出去的） */
  state?: 'all' | 'stock'
  /** 启用状态：all（不限）| active（只算启用中的） */
  active?: 'all' | 'active'
}

/** 账号下别名的分发状态分布，用于"我还剩多少库存" */
export interface PoolBreakdown {
  stock: number
  issued: number
  revoked: number
  inactive: number
  total: number
}

/** 试算结果：只统计不创建，用来在保存前看清"会补几个" */
export interface PoolPreview {
  account_id: string
  current: number
  target: number
  to_create: number
  breakdown: PoolBreakdown
  error?: string
}

export interface PoolConfig {
  account_id: string
  enabled: boolean
  target: number
  /** 已废弃：请用 filter.active。旧配置载入时会自动迁移 */
  count_mode?: 'active' | 'all'
  max_per_run: number
  interval_sec: number
  /** 自动创建的别名归入哪个本地分组（空 = 未分组） */
  group_id?: string
  /** 统计范围 */
  filter?: PoolFilter
  last_check_at?: string
  last_created_at?: string
  last_created?: number
  /** 上次检查时符合筛选条件的数量 */
  last_current?: number
  last_error?: string
  /** 连续失败后自动暂停,需要重新保存配置解除 */
  paused?: boolean
  total_created: number
}

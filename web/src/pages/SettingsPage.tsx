import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { AccountSummary, Group, PoolConfig, SettingsEnv, SettingsResult } from '../api/types'
import AsyncState from '../components/AsyncState'
import PageHeader from '../components/PageHeader'
import PoolDialog from '../components/PoolDialog'
import { useToast } from '../components/ToastProvider'
import {
  IconAccounts,
  IconAliases,
  IconEdit,
  IconInbox,
  IconRefresh,
  IconSettings,
} from '../components/icons'

/** 表单内部用字符串保存数字，避免输入框清空时变成 NaN。 */
interface FormState {
  autoRevokeEnabled: boolean
  intervalMin: string
  mailHours: string
  mailLimit: string
  baseURL: string
}

function toForm(data: SettingsResult): FormState {
  return {
    autoRevokeEnabled: data.settings.auto_revoke_enabled,
    intervalMin: String(data.settings.auto_revoke_interval_min),
    mailHours: String(data.settings.public_mail_hours),
    mailLimit: String(data.settings.public_mail_limit),
    baseURL: data.settings.public_base_url,
  }
}

/**
 * 设置页。
 *
 * 只放"用起来之后才会想调"的东西：库存水位、取件链接回收、取件页默认值、
 * 对外域名。监听地址/数据目录/口令属于部署期配置，改完要重启，只读展示。
 */
export default function SettingsPage() {
  const [form, setForm] = useState<FormState | null>(null)
  // 上次保存成功的快照,用来判断"有没有未保存的改动"
  const [savedForm, setSavedForm] = useState<FormState | null>(null)
  const [env, setEnv] = useState<SettingsEnv | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const [busy, setBusy] = useState(false)
  const [sweeping, setSweeping] = useState(false)
  const [runningPool, setRunningPool] = useState('')

  const [pools, setPools] = useState<PoolConfig[]>([])
  const [poolReady, setPoolReady] = useState(true)
  const [accounts, setAccounts] = useState<AccountSummary[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [poolDialog, setPoolDialog] = useState<{
    open: boolean
    accountId: string
    accountName: string
    config?: PoolConfig | null
  }>({ open: false, accountId: '', accountName: '' })
  const { show } = useToast()

  useEffect(() => {
    let cancelled = false
    Promise.all([
      request<SettingsResult>('/api/settings'),
      // /api/pool 在调度器未启用时返回 503，补号区块降级为"不可用"
      request<{ configs: PoolConfig[] }>('/api/pool').catch(() => null),
      // 账号接口返回的是数组，不是 { accounts: [...] }
      request<AccountSummary[]>('/api/accounts').catch(() => []),
      request<{ groups: Group[] }>('/api/groups').catch(() => ({ groups: [] })),
    ])
      .then(([settings, pool, acc, groupData]) => {
        if (cancelled) return
        setForm(toForm(settings))
        setSavedForm(toForm(settings))
        setEnv(settings.env)
        setPools(pool?.configs ?? [])
        setPoolReady(pool !== null)
        setAccounts(Array.isArray(acc) ? acc : [])
        setGroups(groupData?.groups ?? [])
        setError('')
      })
      .catch((err) => {
        if (cancelled) return
        setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [retryKey])

  function patch(next: Partial<FormState>) {
    setForm((prev) => (prev ? { ...prev, ...next } : prev))
  }

  function reload() {
    setLoading(true)
    setRetryKey((k) => k + 1)
  }

  async function handleSave() {
    if (busy || !form) return
    const interval = Number(form.intervalMin)
    const hours = Number(form.mailHours)
    const limit = Number(form.mailLimit)
    if (!Number.isInteger(interval) || interval < 1 || interval > 1440) {
      show('扫描间隔要填 1-1440 之间的整数（分钟）')
      return
    }
    if (!Number.isInteger(hours) || hours < 1 || hours > 168) {
      show('取件页默认小时数要填 1-168 之间的整数')
      return
    }
    if (!Number.isInteger(limit) || limit < 1 || limit > 50) {
      show('取件页默认条数要填 1-50 之间的整数')
      return
    }
    setBusy(true)
    try {
      await request('/api/settings', {
        method: 'PUT',
        body: JSON.stringify({
          auto_revoke_enabled: form.autoRevokeEnabled,
          auto_revoke_interval_min: interval,
          public_mail_hours: hours,
          public_mail_limit: limit,
          public_base_url: form.baseURL.trim(),
        }),
      })
      show('设置已保存，立即生效')
      setSavedForm(form)
      reload()
    } catch (err) {
      show(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
    }
  }

  async function handleSweep() {
    setSweeping(true)
    try {
      const res = await request<{ revoked: number }>('/api/alias-token/sweep', { method: 'POST' })
      show(res.revoked > 0 ? `已回收 ${res.revoked} 个到期链接` : '当前没有到期的取件链接')
    } catch (err) {
      show(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setSweeping(false)
    }
  }

  async function handlePoolRun(accountId: string) {
    setRunningPool(accountId)
    try {
      await request(`/api/pool/${encodeURIComponent(accountId)}/run`, { method: 'POST' })
      show('已触发一次补齐，稍后看库存水位的变化')
      reload()
    } catch (err) {
      show(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setRunningPool('')
    }
  }

  const poolByAccount = new Map(pools.map((cfg) => [cfg.account_id, cfg]))

  // 只有这几项设置走底部的「保存设置」,补号配置在弹窗里单独保存,
  // 所以给个脏标记,避免以为改完就生效了。
  const dirty = Boolean(
    form && savedForm && JSON.stringify(form) !== JSON.stringify(savedForm),
  )

  /** 把补号的统计范围说成人话，例如「试用客户」。口径固定只数库存，不再展示。 */
  function describeFilter(cfg: PoolConfig): { group: string; inactive: boolean } {
    const groupId = cfg.filter?.group_id
    const group = groupId
      ? (groups.find((g) => g.id === groupId)?.name ?? '已删除的分组')
      : '全部分组'
    // 老配置可能还是 state=all，提示一下可以改回库存口径
    const inactive = cfg.filter?.state === 'all'
    return { group, inactive }
  }

  return (
    <section className="settings-page">
      <PageHeader
        title="设置"
        actions={
          <button onClick={reload} title="重新读取当前配置">
            <IconRefresh size={14} />
            刷新
          </button>
        }
      />

      <AsyncState loading={loading} error={error} empty={false} onRetry={reload}>
        {form && (
          <div className="settings-grid">
            {/* ── 自动补号 ─────────────────────────────── */}
            <Card
              wide
              icon={<IconAliases size={16} />}
              title="自动补号"
              desc="库存低于下限就自动建号补回来，保证随时有号可发。"
              aside={
                <span className="settings-badge">按账号配置 · 弹窗内单独保存</span>
              }
            >
              {!poolReady ? (
                <p className="settings-empty">自动补号功能未启用（启动时没开调度器）。</p>
              ) : accounts.length === 0 ? (
                <p className="settings-empty">
                  还没有账号，先到 <Link to="/accounts">账号</Link> 页添加一个 iCloud 账号。
                </p>
              ) : (
                <div className="table-wrap flat">
                  <table>
                    <thead>
                      <tr>
                        <th>账号</th>
                        <th style={{ width: 190 }}>库存水位</th>
                        <th>规则</th>
                        <th>状态</th>
                        <th style={{ width: 180 }}>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {accounts.map((acc) => {
                        const cfg = poolByAccount.get(acc.id)
                        const rule = cfg ? describeFilter(cfg) : null
                        const badge = !cfg
                          ? 'badge badge-neutral'
                          : cfg.paused
                            ? 'badge badge-pending'
                            : cfg.enabled
                              ? 'badge badge-active'
                              : 'badge badge-neutral'
                        const stateText = !cfg
                          ? '未开启'
                          : cfg.paused
                            ? '已暂停'
                            : cfg.enabled
                              ? '运行中'
                              : '已关闭'
                        return (
                          <tr key={acc.id}>
                            <td>
                              <div className="cell-stack">
                                <span className="cell-strong">{acc.name}</span>
                                <span className="cell-secondary">
                                  {acc.icloud_email || acc.real_email || '—'}
                                </span>
                              </div>
                            </td>
                            <td>
                              {cfg ? (
                                <StockMeter
                                  current={cfg.last_current ?? 0}
                                  target={cfg.target}
                                  checked={Boolean(cfg.last_check_at)}
                                />
                              ) : (
                                <span className="cell-muted">—</span>
                              )}
                            </td>
                            <td>
                              {rule ? (
                                <div className="rule-chips">
                                  <span className="rule-chip">{rule.group}</span>
                                  {cfg?.filter?.active === 'active' ? (
                                    <span className="rule-chip">仅启用中</span>
                                  ) : (
                                    <span className="rule-chip">含停用</span>
                                  )}
                                  {rule.inactive && (
                                    <span className="rule-chip is-warn" title="这条规则还是数全部别名，建议改成只数库存">
                                      含已分发
                                    </span>
                                  )}
                                </div>
                              ) : (
                                <span className="cell-muted">—</span>
                              )}
                            </td>
                            <td>
                              <span className={badge}>{stateText}</span>
                              {cfg && !acc.has_cookies && !acc.has_app_password && (
                                <div className="cell-secondary cell-expired">无凭据，补不了</div>
                              )}
                              {cfg?.last_error && (
                                <div className="cell-secondary cell-expired">{cfg.last_error}</div>
                              )}
                            </td>
                            <td>
                              <div className="row-actions">
                                <button
                                  className={cfg ? '' : 'primary'}
                                  onClick={() =>
                                    setPoolDialog({
                                      open: true,
                                      accountId: acc.id,
                                      accountName: acc.name,
                                      config: cfg ?? null,
                                    })
                                  }
                                >
                                  <IconEdit size={14} />
                                  {cfg ? '配置' : '开启补号'}
                                </button>
                                {cfg && (
                                  <button
                                    disabled={runningPool === acc.id}
                                    title="立刻检查一次，不用等下一个间隔"
                                    onClick={() => void handlePoolRun(acc.id)}
                                  >
                                    <IconRefresh size={14} />
                                    {runningPool === acc.id ? '…' : '补一次'}
                                  </button>
                                )}
                              </div>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              )}
              <p className="settings-foot">
                补号只在有 Cookie 的账号上能跑通；没凭据的账号会一直报「账号未配置 Cookie」，
                先到 <Link to="/accounts">账号</Link> 页更新 Cookie。
              </p>
            </Card>

            {/* ── 自动回收 ─────────────────────────────── */}
            <Card
              icon={<IconRefresh size={16} />}
              title="取件链接自动回收"
              desc="签发出去的链接到期后自动撤销，避免永久有效。"
              aside={
                <label className="switch" title="总开关">
                  <input
                    type="checkbox"
                    checked={form.autoRevokeEnabled}
                    onChange={(e) => patch({ autoRevokeEnabled: e.target.checked })}
                    aria-label="启用自动回收"
                  />
                  <span className="switch-track" />
                </label>
              }
            >
              <div className="settings-field">
                <label className="settings-field-label" htmlFor="set-revoke-interval">
                  检查间隔（分钟）
                </label>
                <input
                  id="set-revoke-interval"
                  type="number"
                  min={1}
                  max={1440}
                  value={form.intervalMin}
                  onChange={(e) => patch({ intervalMin: e.target.value })}
                />
                <span className="settings-field-hint">
                  1–1440 分钟。具体"几天到期"按分组设置在
                  <Link to="/groups">分组</Link>页，这里只管全局开关和扫描频率。
                </span>
              </div>
              <div className="settings-field">
                <button onClick={() => void handleSweep()} disabled={sweeping}>
                  <IconRefresh size={14} />
                  {sweeping ? '回收中…' : '立即检查一次'}
                </button>
                <span className="settings-field-hint">手动跑一轮，效果与后台定时检查相同。</span>
              </div>
            </Card>

            {/* ── 取件页默认值 ─────────────────────────── */}
            <Card
              icon={<IconInbox size={16} />}
              title="取件页默认值"
              desc="对方打开链接时默认展示多少邮件；链接里带参数时以参数为准。"
            >
              <div className="settings-fields-row">
                <div className="settings-field">
                  <label className="settings-field-label" htmlFor="set-mail-hours">
                    时间范围（小时）
                  </label>
                  <input
                    id="set-mail-hours"
                    type="number"
                    min={1}
                    max={168}
                    value={form.mailHours}
                    onChange={(e) => patch({ mailHours: e.target.value })}
                  />
                  <span className="settings-field-hint">1–168 小时（一周）。</span>
                </div>
                <div className="settings-field">
                  <label className="settings-field-label" htmlFor="set-mail-limit">
                    默认条数
                  </label>
                  <input
                    id="set-mail-limit"
                    type="number"
                    min={1}
                    max={50}
                    value={form.mailLimit}
                    onChange={(e) => patch({ mailLimit: e.target.value })}
                  />
                  <span className="settings-field-hint">1–50 封。</span>
                </div>
              </div>
            </Card>

            {/* ── 对外域名 ─────────────────────────────── */}
            <Card
              icon={<IconSettings size={16} />}
              title="对外域名"
              desc="生成分发链接用的站点前缀，填错对方会打不开。"
            >
              <div className="settings-field">
                <label className="settings-field-label" htmlFor="set-base-url">
                  站点前缀
                </label>
                <input
                  id="set-base-url"
                  value={form.baseURL}
                  maxLength={200}
                  placeholder="https://mail.example.com"
                  onChange={(e) => patch({ baseURL: e.target.value.slice(0, 200) })}
                />
                <span className="settings-field-hint">
                  留空表示按你访问本系统的地址自动推导；挂反向代理或自定义域名时必须填公网地址，
                  否则发出去的链接会指向内网。
                  {env?.public_base_url_env ? (
                    <>
                      <br />
                      部署时已用环境变量指定了 <code>{env.public_base_url_env}</code>，
                      这里留空即沿用它，填了以这里为准。
                    </>
                  ) : null}
                </span>
              </div>
            </Card>

            {/* ── 部署信息 ─────────────────────────────── */}
            <Card
              icon={<IconAccounts size={16} />}
              title="部署信息"
              desc="由启动参数或环境变量决定，改完要重启，这里只读。"
              muted
            >
              <dl className="settings-meta">
                <div>
                  <dt>版本</dt>
                  <dd>{env?.version || 'dev'}</dd>
                </div>
                <div>
                  <dt>数据目录</dt>
                  <dd className="mono">{env?.data_dir || '—（未配置）'}</dd>
                </div>
                <div>
                  <dt>HTTPS</dt>
                  <dd>{env?.secure_cookie ? '已启用' : '未启用'}</dd>
                </div>
              </dl>
            </Card>
          </div>
        )}
      </AsyncState>

      {dirty && (
        <div className="settings-savebar" role="status">
          <span className="settings-savebar-text">设置已改动，尚未保存</span>
          <button onClick={() => setForm(savedForm)} disabled={busy}>
            放弃
          </button>
          <button className="primary" onClick={() => void handleSave()} disabled={busy}>
            {busy ? '保存中…' : '保存设置'}
          </button>
        </div>
      )}

      <PoolDialog
        key={poolDialog.accountId || 'pool'}
        open={poolDialog.open}
        accountId={poolDialog.accountId}
        accountName={poolDialog.accountName}
        config={poolDialog.config}
        onClose={() => setPoolDialog({ open: false, accountId: '', accountName: '' })}
        onSaved={(message) => {
          setPoolDialog({ open: false, accountId: '', accountName: '' })
          show(message)
          reload()
        }}
      />
    </section>
  )
}

/** 统一的设置卡片：图标 + 标题 + 一句话说明 + 右侧控件 */
function Card({
  icon,
  title,
  desc,
  aside,
  wide,
  muted,
  children,
}: {
  icon: ReactNode
  title: string
  desc?: string
  aside?: ReactNode
  wide?: boolean
  muted?: boolean
  children: ReactNode
}) {
  return (
    <section className={`settings-card${wide ? ' is-wide' : ''}${muted ? ' is-muted' : ''}`}>
      <header className="settings-card-head">
        <span className="settings-card-icon">{icon}</span>
        <div className="settings-card-title">
          <h2>{title}</h2>
          {desc && <p>{desc}</p>}
        </div>
        {aside && <div className="settings-card-aside">{aside}</div>}
      </header>
      <div className="settings-card-body">{children}</div>
    </section>
  )
}

/** 库存水位条：一眼看出"够不够发" */
function StockMeter({
  current,
  target,
  checked,
}: {
  current: number
  target: number
  checked: boolean
}) {
  if (!checked) {
    return (
      <span className="cell-muted" title="还没跑过第一轮检查">
        待检查 / {target}
      </span>
    )
  }
  const pct = Math.min(100, Math.round((current / Math.max(1, target)) * 100))
  const tone = current === 0 ? 'is-empty' : current >= target ? 'is-ok' : 'is-low'
  return (
    <div className="stock-meter" title={`可用库存 ${current}，下限 ${target}`}>
      <div className="stock-meter-track">
        <div className={`stock-meter-fill ${tone}`} style={{ width: `${pct}%` }} />
      </div>
      <span className="stock-meter-text">
        {current}
        <span className="cell-muted"> / {target}</span>
      </span>
    </div>
  )
}

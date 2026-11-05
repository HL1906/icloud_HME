import { useCallback, useEffect, useRef, useState } from 'react'
import Dialog from './Dialog'
import { request, ApiError } from '../api/client'
import type { Group, PoolConfig, PoolPreview } from '../api/types'

interface PoolDialogProps {
  accountId: string
  accountName: string
  config?: PoolConfig | null
  open: boolean
  onClose: () => void
  /** 保存/触发/关闭成功后通知父级刷新并提示 */
  onSaved: (message: string) => void
}

/**
 * 自动补号设置。
 *
 * 心智模型只有一句话：**让可用库存始终不低于 N**。
 * 分发出去一个，库存就少一个；低于 N 就自动补回 N。
 * 所以这里的主输入是"库存下限"，其余都是限定"数哪些号"。
 *
 * 统计口径固定为「只数库存」：已分发 / 已回收 / 已停用都不计入 ——
 * 它们只会越积越多，补号无法让它们达标，也无法回答"还剩多少可以发"。
 */
export default function PoolDialog({
  accountId,
  accountName,
  config,
  open,
  onClose,
  onSaved,
}: PoolDialogProps) {
  // 表单初值直接取自 props:对话框由父级条件渲染,关闭即卸载,
  // 每次打开都是一次全新挂载,不需要 effect 回填。
  const [enabled, setEnabled] = useState(config?.enabled ?? true)
  const [target, setTarget] = useState(String(config?.target ?? 20))
  const [maxPerRun, setMaxPerRun] = useState(String(config?.max_per_run ?? 3))
  // 间隔用分钟展示，提交时换算成秒（后端只认秒）
  const [intervalMin, setIntervalMin] = useState(
    String(Math.round((config?.interval_sec ?? 300) / 60)),
  )
  // 新配置默认把"已停用的"排除在库存外：停用的号发出去也收不到信
  const [onlyActive, setOnlyActive] = useState(
    config ? config.filter?.active === 'active' : true,
  )
  const [filterGroupId, setFilterGroupId] = useState(config?.filter?.group_id ?? '')
  const [groupId, setGroupId] = useState(config?.group_id ?? '')
  const [groups, setGroups] = useState<Group[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // 试算：当前有几个、会补几个
  const [preview, setPreview] = useState<PoolPreview | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const previewSeq = useRef(0)

  // 按分组统计时,新邮箱必须进同一个分组(否则补出来也不计入,会无限补)
  const effectiveGroupId = filterGroupId || groupId

  useEffect(() => {
    let cancelled = false
    request<{ groups: Group[] }>('/api/groups')
      .then((data) => {
        if (!cancelled) setGroups(data.groups ?? [])
      })
      .catch(() => {
        // 忽略:下拉为空时仍可保存"不分组"
      })
    return () => {
      cancelled = true
    }
  }, [])

  const runPreview = useCallback(async () => {
    const seq = ++previewSeq.current
    setPreviewing(true)
    try {
      const query = new URLSearchParams({
        state: 'stock',
        active: onlyActive ? 'active' : 'all',
        group_id: filterGroupId,
        target: target || '0',
      })
      const data = await request<PoolPreview>(
        `/api/pool/${encodeURIComponent(accountId)}/preview?${query.toString()}`,
      )
      if (seq === previewSeq.current) setPreview(data)
    } catch {
      if (seq === previewSeq.current) setPreview(null)
    } finally {
      if (seq === previewSeq.current) setPreviewing(false)
    }
  }, [accountId, onlyActive, filterGroupId, target])

  // 条件一变就重新试算，节流 400ms 免得每敲一个数字都打一次上游
  useEffect(() => {
    const timer = setTimeout(() => void runPreview(), 400)
    return () => clearTimeout(timer)
  }, [runPreview])

  async function handleSave() {
    if (busy) return
    const targetNum = Number(target)
    const maxNum = Number(maxPerRun)
    const intervalSec = Math.round(Number(intervalMin) * 60)
    if (!Number.isInteger(targetNum) || targetNum < 1 || targetNum > 200) {
      setError('库存下限需在 1–200 之间')
      return
    }
    if (!Number.isInteger(maxNum) || maxNum < 1 || maxNum > 10) {
      setError('单轮上限需在 1–10 之间')
      return
    }
    if (!Number.isFinite(intervalSec) || intervalSec < 60 || intervalSec > 86400) {
      setError('检查间隔需在 1–1440 分钟之间')
      return
    }

    setBusy(true)
    setError('')
    try {
      await request(`/api/pool/${encodeURIComponent(accountId)}`, {
        method: 'PUT',
        body: JSON.stringify({
          enabled,
          target: targetNum,
          max_per_run: maxNum,
          interval_sec: intervalSec,
          group_id: effectiveGroupId,
          filter: {
            group_id: filterGroupId,
            state: 'stock',
            active: onlyActive ? 'active' : 'all',
          },
        }),
      })
      onSaved(enabled ? '自动补号已保存' : '自动补号已关闭')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
    }
  }

  async function handleRunNow() {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await request(`/api/pool/${encodeURIComponent(accountId)}/run`, { method: 'POST' })
      onSaved('已触发一次补齐，稍后查看结果')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
    }
  }

  async function handleDisable() {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await request(`/api/pool/${encodeURIComponent(accountId)}`, { method: 'DELETE' })
      onSaved('已停止该账号的自动补号')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog title={`自动补号 · ${accountName}`} open={open} onClose={onClose}>
      {error && (
        <div className="alert-error" role="alert">
          {error}
        </div>
      )}

      <div className="pool-hero">
        <div className="pool-hero-main">
          <label className="pool-hero-label" htmlFor="pool-target">
            可用库存不低于
          </label>
          <div className="pool-hero-input">
            <input
              id="pool-target"
              type="number"
              min={1}
              max={200}
              value={target}
              onChange={(e) => setTarget(e.target.value)}
            />
            <span className="pool-hero-unit">个</span>
          </div>
        </div>
        <p className="pool-hero-desc">
          分发一个就少一个，低于这个数自动补回来；已发出去的链接不受影响。
        </p>
      </div>

      <PreviewBox preview={preview} loading={previewing} />

      <fieldset className="pool-section">
        <legend>数哪些邮箱</legend>
        <p className="pool-scope-line">
          <b>只数还没发出去的库存</b>
          <span>已分发、已回收、已停用的都不计入 —— 这样才是"还剩多少可以发"</span>
        </p>

        <div className="pool-inline-fields">
          <div className="pool-field">
            <label htmlFor="pool-filter-group">限定分组</label>
            <select
              id="pool-filter-group"
              value={filterGroupId}
              onChange={(e) => setFilterGroupId(e.target.value)}
            >
              <option value="">不限分组</option>
              {groups.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
            </select>
          </div>
          <label className="pool-check">
            <input
              type="checkbox"
              checked={onlyActive}
              onChange={(e) => setOnlyActive(e.target.checked)}
            />
            <span>
              已停用的不算库存
              <em>停用的号发出去也收不到信</em>
            </span>
          </label>
        </div>
      </fieldset>

      <fieldset className="pool-section">
        <legend>补号节奏</legend>
        <div className="pool-inline-fields">
          <div className="pool-field">
            <label htmlFor="pool-max">每轮最多补</label>
            <input
              id="pool-max"
              type="number"
              min={1}
              max={10}
              value={maxPerRun}
              onChange={(e) => setMaxPerRun(e.target.value)}
            />
            <span className="hint">个，避免一次建太多</span>
          </div>
          <div className="pool-field">
            <label htmlFor="pool-interval">检查间隔</label>
            <input
              id="pool-interval"
              type="number"
              min={1}
              max={1440}
              value={intervalMin}
              onChange={(e) => setIntervalMin(e.target.value)}
            />
            <span className="hint">分钟看一次</span>
          </div>
          <div className="pool-field">
            <label htmlFor="pool-group">新号归入</label>
            <select
              id="pool-group"
              value={effectiveGroupId}
              disabled={Boolean(filterGroupId)}
              onChange={(e) => setGroupId(e.target.value)}
            >
              <option value="">不分组</option>
              {groups.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
            </select>
          </div>
        </div>
        <p className="hint">
          {filterGroupId
            ? '已限定分组，新补的号会自动归入同一个分组（否则它们不算进库存，会一直补）。'
            : '自动补号不写 iCloud 标签（标签建号后改不了），只在本系统里按分组归档。'}
        </p>
      </fieldset>

      <div className="form-field">
        <label className="pool-check" htmlFor="pool-enabled">
          <input
            id="pool-enabled"
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          <span>
            启用自动补号
            <em>关掉只停调度，配置保留</em>
          </span>
        </label>
      </div>

      {config && (
        <div className="pool-status">
          <span>上次检查：{config.last_check_at ? formatTime(config.last_check_at) : '尚未检查'}</span>
          <span>累计补号：{config.total_created} 个</span>
          {config.paused && <span className="pool-status-paused">已自动暂停</span>}
        </div>
      )}
      {config?.last_error && (
        <div className="alert-error" role="alert" style={{ marginBottom: 12 }}>
          {config.last_error}
        </div>
      )}

      <div className="form-actions pool-dialog-actions">
        {config && (
          <button className="danger" onClick={() => void handleDisable()} disabled={busy}>
            停止自动补号
          </button>
        )}
        {config && (
          <button onClick={() => void handleRunNow()} disabled={busy}>
            立即补一次
          </button>
        )}
        <button onClick={onClose} disabled={busy}>
          取消
        </button>
        <button className="primary" onClick={() => void handleSave()} disabled={busy}>
          {busy ? '处理中…' : '保存'}
        </button>
      </div>
    </Dialog>
  )
}

/** 试算结果条：当前库存 → 会不会补、补几个 */
function PreviewBox({ preview, loading }: { preview: PoolPreview | null; loading: boolean }) {
  if (loading && !preview) {
    return <div className="pool-preview pool-preview-loading">正在统计当前库存…</div>
  }
  if (!preview) {
    return (
      <div className="pool-preview pool-preview-loading">
        暂时读不到当前库存（账号可能还没配置 Cookie）
      </div>
    )
  }
  if (preview.error) {
    return <div className="pool-preview pool-preview-warn">{preview.error}</div>
  }
  const b = preview.breakdown
  const enough = preview.to_create === 0
  return (
    <div className={`pool-preview ${enough ? 'pool-preview-ok' : 'pool-preview-warn'}`}>
      <div className="pool-preview-main">
        {enough ? (
          <>
            当前 <b>{preview.current}</b> 个，已达到 {preview.target} 个，不用补
          </>
        ) : (
          <>
            当前 <b>{preview.current}</b> 个 → 低于 {preview.target}，本轮会补{' '}
            <b>{preview.to_create}</b> 个
          </>
        )}
      </div>
      <div className="pool-preview-breakdown">
        库存 {b.stock} · 已分发 {b.issued} · 已回收 {b.revoked}
        {b.inactive > 0 ? ` · 其中停用 ${b.inactive}` : ''} · 共 {b.total}
      </div>
    </div>
  )
}

/** 把 RFC3339 时间格式化为「月-日 时:分」，解析失败时原样返回 */
function formatTime(raw: string): string {
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return raw
  const pad = (v: number) => String(v).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

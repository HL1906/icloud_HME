import { useEffect, useState } from 'react'
import Dialog from './Dialog'
import { request, ApiError } from '../api/client'
import type { Group, PoolConfig } from '../api/types'

interface PoolDialogProps {
  accountId: string
  accountName: string
  config?: PoolConfig | null
  open: boolean
  onClose: () => void
  /** 保存/触发/关闭成功后通知父级刷新并提示 */
  onSaved: (message: string) => void
}

/** 自动补号设置：目标数量、口径、单轮上限、检查间隔 */
export default function PoolDialog({
  accountId,
  accountName,
  config,
  open,
  onClose,
  onSaved,
}: PoolDialogProps) {
  // 表单初值直接取自 props:对话框由父级条件渲染,关闭即卸载,
  // 每次打开都是一次全新挂载,不需要 effect 回填(回填反而会多一次渲染)。
  const [enabled, setEnabled] = useState(config?.enabled ?? true)
  const [target, setTarget] = useState(String(config?.target ?? 20))
  const [countMode, setCountMode] = useState<'active' | 'all'>(
    config?.count_mode === 'all' ? 'all' : 'active',
  )
  const [maxPerRun, setMaxPerRun] = useState(String(config?.max_per_run ?? 3))
  // 间隔用分钟展示，提交时换算成秒（后端只认秒）
  const [intervalMin, setIntervalMin] = useState(
    String(Math.round((config?.interval_sec ?? 300) / 60)),
  )
  const [groupId, setGroupId] = useState(config?.group_id ?? '')
  const [groups, setGroups] = useState<Group[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  // 分组用于"自动创建的邮箱归到哪一类"。拉取失败不阻塞补号配置本身。
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

  async function handleSave() {
    if (busy) return
    const targetNum = Number(target)
    const maxNum = Number(maxPerRun)
    const intervalSec = Math.round(Number(intervalMin) * 60)
    if (!Number.isInteger(targetNum) || targetNum < 1 || targetNum > 200) {
      setError('目标数量需在 1–200 之间')
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
          count_mode: countMode,
          max_per_run: maxNum,
          interval_sec: intervalSec,
          group_id: groupId,
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

      {config && (
        <div className="pool-status">
          <span>
            上次检查：{config.last_check_at ? formatTime(config.last_check_at) : '尚未检查'}
          </span>
          <span>累计创建：{config.total_created} 个</span>
          {config.last_created ? <span>上次补号：{config.last_created} 个</span> : null}
          {config.paused && <span className="pool-status-paused">已自动暂停</span>}
        </div>
      )}
      {config?.last_error && (
        <div className="alert-error" role="alert" style={{ marginBottom: 12 }}>
          {config.last_error}
        </div>
      )}

      <p className="hint" style={{ marginBottom: 12 }}>
        后台每轮检查该账号的别名数量，低于目标就自动创建补齐。单轮最多创建「单轮上限」个，
        两次创建之间有间隔，不会一次打满。
      </p>

      <div className="form-field">
        <label htmlFor="pool-enabled" className="inline-check">
          <input
            id="pool-enabled"
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          启用自动补号
        </label>
      </div>

      <div className="form-grid">
        <div className="form-field">
          <label htmlFor="pool-target">目标数量</label>
          <input
            id="pool-target"
            type="number"
            min={1}
            max={200}
            value={target}
            onChange={(e) => setTarget(e.target.value)}
          />
        </div>
        <div className="form-field">
          <label htmlFor="pool-mode">统计口径</label>
          <select
            id="pool-mode"
            value={countMode}
            onChange={(e) => setCountMode(e.target.value === 'all' ? 'all' : 'active')}
          >
            <option value="active">只算已启用</option>
            <option value="all">算全部（含停用）</option>
          </select>
        </div>
        <div className="form-field">
          <label htmlFor="pool-max">单轮上限</label>
          <input
            id="pool-max"
            type="number"
            min={1}
            max={10}
            value={maxPerRun}
            onChange={(e) => setMaxPerRun(e.target.value)}
          />
        </div>
        <div className="form-field">
          <label htmlFor="pool-interval">检查间隔（分钟）</label>
          <input
            id="pool-interval"
            type="number"
            min={1}
            max={1440}
            value={intervalMin}
            onChange={(e) => setIntervalMin(e.target.value)}
          />
        </div>
      </div>

      <div className="form-field" style={{ marginTop: 12 }}>
        <label htmlFor="pool-group">自动归入分组</label>
        <select id="pool-group" value={groupId} onChange={(e) => setGroupId(e.target.value)}>
          <option value="">不分组</option>
          {groups.map((g) => (
            <option key={g.id} value={g.id}>
              {g.name}
            </option>
          ))}
        </select>
        <span className="hint">
          自动创建的邮箱不写 iCloud 标签（标签是官方字段，留空即可），只在本系统里按分组归档。
          分组可以到「分组」页新建。
        </span>
      </div>

      <div className="form-actions">
        {config && (
          <button className="danger" onClick={() => void handleDisable()} disabled={busy}>
            停止自动补号
          </button>
        )}
        {config && (
          <button onClick={() => void handleRunNow()} disabled={busy}>
            立即补齐一次
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

/** 把 RFC3339 时间格式化为「月-日 时:分」，解析失败时原样返回 */
function formatTime(raw: string): string {
  const date = new Date(raw)
  if (Number.isNaN(date.getTime())) return raw
  const pad = (v: number) => String(v).padStart(2, '0')
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`
}

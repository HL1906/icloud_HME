import { http, HttpResponse } from 'msw'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ChangePasswordDialog, { passwordStrength } from './ChangePasswordDialog'
import { server } from '../test/server'
import { setCSRFToken } from '../api/client'
import { ToastProvider } from './ToastProvider'

function renderDialog(onChanged = vi.fn()) {
  return render(
    <ToastProvider>
      <ChangePasswordDialog open onClose={() => {}} onChanged={onChanged} />
    </ToastProvider>,
  )
}

describe('passwordStrength', () => {
  it('空密码无提示,弱密码等级低,强密码等级高', () => {
    expect(passwordStrength('').level).toBe(0)
    expect(passwordStrength('abc').level).toBeLessThan(3)
    expect(passwordStrength('Str0ng!Passw0rd2026').level).toBe(4)
  })
})

describe('ChangePasswordDialog', () => {
  beforeEach(() => {
    setCSRFToken('csrf-test')
    server.resetHandlers()
  })

  it('两次新密码不一致时提示且不发请求', async () => {
    let called = false
    server.use(
      http.post('/api/auth/password', () => {
        called = true
        return HttpResponse.json({ success: true, data: { changed: true } })
      }),
    )
    renderDialog()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/^当前密码$/), 'OldPass2026')
    await user.type(screen.getByLabelText(/^新密码$/), 'NewPass2026x')
    await user.type(screen.getByLabelText(/^确认新密码$/), 'Different2026')
    await user.click(screen.getByRole('button', { name: '确认修改' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('两次输入的新密码不一致')
    expect(called).toBe(false)
  })

  it('新密码过短时提示且不发请求', async () => {
    let called = false
    server.use(
      http.post('/api/auth/password', () => {
        called = true
        return HttpResponse.json({ success: true, data: { changed: true } })
      }),
    )
    renderDialog()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/^当前密码$/), 'OldPass2026')
    await user.type(screen.getByLabelText(/^新密码$/), 'abc')
    await user.type(screen.getByLabelText(/^确认新密码$/), 'abc')
    await user.click(screen.getByRole('button', { name: '确认修改' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('至少 8 位')
    expect(called).toBe(false)
  })

  it('提交成功后回调', async () => {
    let body: unknown = null
    server.use(
      http.post('/api/auth/password', async ({ request }) => {
        body = await request.json()
        return HttpResponse.json({ success: true, data: { changed: true } })
      }),
    )
    const onChanged = vi.fn()
    renderDialog(onChanged)
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/^当前密码$/), 'OldPass2026')
    await user.type(screen.getByLabelText(/^新密码$/), 'NewPass2026x')
    await user.type(screen.getByLabelText(/^确认新密码$/), 'NewPass2026x')
    await user.click(screen.getByRole('button', { name: '确认修改' }))

    await waitFor(() => expect(onChanged).toHaveBeenCalled())
    expect(body).toMatchObject({ old_password: 'OldPass2026', new_password: 'NewPass2026x' })
  })

  it('服务端拒绝时显示错误信息', async () => {
    server.use(
      http.post('/api/auth/password', () =>
        HttpResponse.json(
          { success: false, code: 'INVALID_CREDENTIALS', message: '当前密码不正确' },
          { status: 401 },
        ),
      ),
    )
    renderDialog()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText(/^当前密码$/), 'WrongOld2026')
    await user.type(screen.getByLabelText(/^新密码$/), 'NewPass2026x')
    await user.type(screen.getByLabelText(/^确认新密码$/), 'NewPass2026x')
    await user.click(screen.getByRole('button', { name: '确认修改' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('当前密码不正确')
  })
})

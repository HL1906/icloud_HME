import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth/AuthProvider'
import { ToastProvider } from './components/ToastProvider'
import AppShell from './components/AppShell'
import LoginPage from './pages/LoginPage'
import DashboardPage from './pages/DashboardPage'
import AccountsPage from './pages/AccountsPage'
import AliasesPage from './pages/AliasesPage'
import GroupsPage from './pages/GroupsPage'
import InboxPage from './pages/InboxPage'

function ProtectedLayout() {
  const { status } = useAuth()
  if (status === 'checking') {
    return <p className="empty-state" aria-busy="true">加载中…</p>
  }
  if (status === 'anonymous') {
    return <Navigate to="/login" replace />
  }
  return <AppShell />
}

export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <ToastProvider>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route element={<ProtectedLayout />}>
              <Route path="/dashboard" element={<DashboardPage />} />
              <Route path="/accounts" element={<AccountsPage />} />
              <Route path="/aliases" element={<AliasesPage />} />
              <Route path="/groups" element={<GroupsPage />} />
              <Route path="/inbox" element={<InboxPage />} />
              <Route path="*" element={<Navigate to="/dashboard" replace />} />
            </Route>
          </Routes>
        </ToastProvider>
      </AuthProvider>
    </BrowserRouter>
  )
}

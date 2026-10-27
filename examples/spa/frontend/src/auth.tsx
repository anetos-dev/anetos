import { createContext, use, useEffect, useState, type ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router'
import { api, errorText, isStatus } from './api'
import type { UserResponse } from './types/api'

type Auth = {
  // user is the logged-in user, or null.
  user: UserResponse | null
  // setUser records a login (the user), a logout (null) or a change.
  setUser: (user: UserResponse | null) => void
}

const AuthContext = createContext<Auth | null>(null)

// AuthProvider asks the API who is logged in (the session's user: GET
// /api/v1/me, 401 for nobody), then shows its children.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<UserResponse | null>()
  const [failure, setFailure] = useState('')
  useEffect(() => {
    api.me().then(setUser, (err: unknown) => {
      if (isStatus(err, 401)) setUser(null)
      else setFailure(errorText(err))
    })
  }, [])
  if (failure) {
    return (
      <main>
        <p className="error">The API didn't answer: {failure}</p>
        <button type="button" onClick={() => location.reload()}>Try again</button>
      </main>
    )
  }
  if (user === undefined) return null
  return <AuthContext value={{ user, setUser }}>{children}</AuthContext>
}

export function useAuth(): Auth {
  const auth = use(AuthContext)
  if (!auth) throw new Error('useAuth needs an AuthProvider')
  return auth
}

// RequireUser shows its children to a logged-in user; others log in
// first, then come back.
export function RequireUser({ children }: { children: ReactNode }) {
  const { user } = useAuth()
  const location = useLocation()
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />
  return children
}

// GuestOnly shows its children (the login and registration pages) to
// visitors; a logged-in user goes on: to the page RequireUser sent them
// from, or the bookmarks. Logging in is setting the user.
export function GuestOnly({ children }: { children: ReactNode }) {
  const { user } = useAuth()
  const from: string = useLocation().state?.from ?? '/'
  if (user) return <Navigate to={from} replace />
  return children
}

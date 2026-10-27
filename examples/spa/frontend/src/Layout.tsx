import { useState } from 'react'
import { Link, NavLink, Outlet, useNavigate } from 'react-router'
import { api, errorText, isStatus } from './api'
import { useAuth } from './auth'

// Layout is every page's frame: the header, with the user's links, and a
// reminder to verify their address.
export function Layout() {
  const { user, setUser } = useAuth()
  const navigate = useNavigate()
  const [failure, setFailure] = useState('')
  async function logOut() {
    try {
      await api.logout()
    } catch (err) {
      if (!isStatus(err, 401)) { // 401: logged out already
        setFailure(errorText(err))
        return
      }
    }
    setFailure('')
    setUser(null)
    navigate('/login')
  }
  return (
    <>
      <header>
        <Link to="/" className="brand">Bookmarks</Link>
        {user && (
          <nav aria-label="Account">
            <NavLink to="/" end>Bookmarks</NavLink>
            <NavLink to="/archived">Archived</NavLink>
            <span className="muted">{user.name}</span>
            <button type="button" className="link" onClick={logOut}>Log out</button>
          </nav>
        )}
      </header>
      <main>
        {failure && (
          <p className="error" role="alert">
            {failure}
          </p>
        )}
        {user && !user.email_verified_at && <VerifyReminder />}
        <Outlet />
      </main>
    </>
  )
}

// VerifyReminder asks the user to open the link the API emailed them,
// and sends another.
function VerifyReminder() {
  const [sent, setSent] = useState('')
  async function send() {
    try {
      await api.verification.send()
      setSent('We sent another.')
    } catch (err) {
      setSent(errorText(err)) // 429: too many emails
    }
  }
  return (
    <p className="notice" role="status">
      Please verify your email address with the link we sent you.{' '}
      {sent || <button type="button" className="link" onClick={send}>Send it again</button>}
    </p>
  )
}

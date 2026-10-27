import { useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ApiError } from '../types/api'
import { api, errorText } from '../api'
import { useAuth } from '../auth'

// VerifyEmail is the page of the emailed verification link
// (/verify-email?token=…): it sends the token to the API as it opens.
export function VerifyEmail() {
  const [params] = useSearchParams()
  const { user, setUser } = useAuth()
  const [verified, setVerified] = useState(false)
  const [failure, setFailure] = useState('')
  const sent = useRef(false) // once, though StrictMode runs effects twice
  useEffect(() => {
    if (sent.current) return
    sent.current = true
    api.verification
      .verify({ token: params.get('token') ?? '' })
      .then(() => {
        setVerified(true)
        if (user) api.me().then(setUser, () => {}) // email_verified_at is set now
      })
      .catch((err: unknown) =>
        // A bad or expired link is a 400 without details; one for an
        // address changed since says so.
        setFailure(
          err instanceof ApiError && err.status === 400 && !err.problem.detail ? 'The link is invalid or has expired.' : errorText(err),
        ),
      )
  }, [params, user, setUser])
  if (verified) {
    return (
      <>
        <h1>Your email address is verified</h1>
        <p>
          <Link to="/">Go to your bookmarks</Link>
        </p>
      </>
    )
  }
  if (!failure) return <p>Verifying your email address…</p>
  return (
    <>
      <h1>The link didn't work</h1>
      <p className="error">{failure}</p>
      <p>Links expire: log in to have another sent.</p>
    </>
  )
}

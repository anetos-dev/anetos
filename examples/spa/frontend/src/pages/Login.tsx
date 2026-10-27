import { useState } from 'react'
import { Link } from 'react-router'
import { api, tokenAnswer } from '../api'
import { useAuth } from '../auth'
import { Field, Form } from '../form'
import type { LoginResponse } from '../types/api'

// Login logs in with an email address and a password, then, for a user
// with two-factor authentication, a code of their authenticator app or a
// recovery code. The session holds the login between the two steps;
// once logged in, GuestOnly sends the user on.
export function Login() {
  const { setUser } = useAuth()
  const [step, setStep] = useState<'password' | 'code'>('password')
  const done = (res: LoginResponse) => {
    if (res.token) throw new Error(tokenAnswer)
    setUser(res.user ?? null)
  }
  if (step === 'code') {
    return (
      <>
        <h1>Two-factor authentication</h1>
        <Form submit="Log in" send={async (v) => done(await api.loginTwoFactor({ code: String(v.get('code')) }))}>
          <Field name="code" label="A code of your authenticator app, or a recovery code" autoComplete="one-time-code" autoFocus />
        </Form>
        <p>
          {/* After 10 minutes, the API asks to log in again. */}
          <button type="button" className="link" onClick={() => setStep('password')}>Start over</button>
        </p>
      </>
    )
  }
  return (
    <>
      <h1>Log in</h1>
      <Form
        submit="Log in"
        send={async (v) => {
          const res = await api.login({ email: String(v.get('email')), password: String(v.get('password')) })
          if (res.two_factor) setStep('code')
          else done(res)
        }}
      >
        <Field name="email" label="Email" type="email" autoComplete="username" autoFocus />
        <Field name="password" label="Password" type="password" autoComplete="current-password" />
      </Form>
      <p>
        <Link to="/forgot-password">Forgot your password?</Link> · <Link to="/register">Create an account</Link>
      </p>
    </>
  )
}

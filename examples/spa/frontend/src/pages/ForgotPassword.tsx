import { useState } from 'react'
import { Link } from 'react-router'
import { api } from '../api'
import { Field, Form } from '../form'

// ForgotPassword asks the API to email a password reset link (to
// /reset-password). The API answers the same whether or not the address
// has an account.
export function ForgotPassword() {
  const [sent, setSent] = useState(false)
  if (sent) {
    return (
      <>
        <h1>Check your email</h1>
        <p>If an account has that address, we sent it a link to choose a new password.</p>
      </>
    )
  }
  return (
    <>
      <h1>Forgot your password?</h1>
      <Form
        submit="Email me a link"
        send={async (v) => {
          await api.password.email({ email: String(v.get('email')) })
          setSent(true)
        }}
      >
        <Field name="email" label="Email" type="email" autoComplete="username" autoFocus />
      </Form>
      <p>
        <Link to="/login">Back to the login</Link>
      </p>
    </>
  )
}

import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { api } from '../api'
import { Field, Form } from '../form'

// ResetPassword is the page of the emailed reset link
// (/reset-password?token=…): it sets the new password with the link's
// token, which works once.
export function ResetPassword() {
  const [params] = useSearchParams()
  const [done, setDone] = useState(false)
  if (done) {
    return (
      <>
        <h1>Your password is changed</h1>
        <p>
          <Link to="/login">Log in</Link> with it.
        </p>
      </>
    )
  }
  return (
    <>
      <h1>Choose a new password</h1>
      <Form
        submit="Change the password"
        send={async (v) => {
          await api.password.reset({
            token: params.get('token') ?? '',
            password: String(v.get('password')),
            password_confirmation: String(v.get('password_confirmation')),
          })
          setDone(true)
        }}
      >
        <Field name="password" label="New password (8 characters or more)" type="password" autoComplete="new-password" autoFocus />
        <Field name="password_confirmation" label="The password again" type="password" autoComplete="new-password" />
      </Form>
    </>
  )
}

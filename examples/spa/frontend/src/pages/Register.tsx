import { Link } from 'react-router'
import { api, tokenAnswer } from '../api'
import { useAuth } from '../auth'
import { Field, Form } from '../form'

// Register creates an account, logged in; the API emails a link to verify
// the address (to /verify-email).
export function Register() {
  const { setUser } = useAuth()
  return (
    <>
      <h1>Create an account</h1>
      <Form
        submit="Create the account"
        send={async (v) => {
          const res = await api.register({
            name: String(v.get('name')),
            email: String(v.get('email')),
            password: String(v.get('password')),
            password_confirmation: String(v.get('password_confirmation')),
          })
          if (res.token) throw new Error(tokenAnswer)
          setUser(res.user ?? null)
        }}
      >
        <Field name="name" label="Name" autoComplete="name" autoFocus />
        <Field name="email" label="Email" type="email" autoComplete="username" />
        <Field name="password" label="Password (8 characters or more)" type="password" autoComplete="new-password" />
        <Field name="password_confirmation" label="The password again" type="password" autoComplete="new-password" />
      </Form>
      <p>
        <Link to="/login">Log in instead</Link>
      </p>
    </>
  )
}

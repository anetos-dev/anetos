import { createContext, use, useEffect, useId, useRef, useState, type FormEvent, type InputHTMLAttributes, type ReactNode } from 'react'
import { errorMessages } from './api'

// The API's messages by field ("" for the form's own).
const ErrorsContext = createContext<Record<string, string>>({})

type FormProps = {
  // submit is the button's text.
  submit: string
  // send sends the form's values to the API; what it throws shows in the
  // form.
  send: (values: FormData) => Promise<void>
  children: ReactNode
}

// Form calls send with its values, and shows the API's errors: a 422's
// message by its field, others above the button.
export function Form({ submit, send, children }: FormProps) {
  const [messages, setMessages] = useState<Record<string, string>>({})
  const [others, setOthers] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  const form = useRef<HTMLFormElement>(null)
  async function onSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    if (busy) return // the button stays enabled: it keeps the focus
    const el = e.currentTarget
    const values = new FormData(el)
    setBusy(true)
    setMessages({})
    setOthers([])
    try {
      await send(values)
    } catch (err) {
      const msgs = errorMessages(err)
      // The messages no field shows: a link's "token", or the error's own.
      const fields = new Set(Array.from(el.elements, (f) => (f as HTMLInputElement).name))
      setMessages(msgs)
      setOthers(Object.entries(msgs).filter(([name]) => !fields.has(name)).map(([, m]) => m))
    } finally {
      setBusy(false)
    }
  }
  // After an error, the first field it names takes the focus.
  useEffect(() => {
    form.current?.querySelector<HTMLInputElement>('[aria-invalid]')?.focus()
  }, [messages])
  return (
    <ErrorsContext value={messages}>
      <form ref={form} onSubmit={onSubmit}>
        {children}
        <p className="error" role="alert">
          {others.join(' ')}
        </p>
        <button aria-disabled={busy || undefined}>{submit}</button>
      </form>
    </ErrorsContext>
  )
}

type FieldProps = InputHTMLAttributes<HTMLInputElement> & { name: string; label: string }

// Field is an input of a Form, with its label and its error, which the
// input refers to (aria-describedby).
export function Field({ name, label, type = 'text', ...props }: FieldProps) {
  const message = use(ErrorsContext)[name]
  const id = useId()
  return (
    <>
      <label htmlFor={id}>{label}</label>
      <input
        id={id}
        name={name}
        type={type}
        aria-invalid={message ? true : undefined}
        aria-describedby={message ? `${id}-error` : undefined}
        required
        {...props}
      />
      {message && (
        <span id={`${id}-error`} className="error field-error">
          {message}
        </span>
      )}
    </>
  )
}

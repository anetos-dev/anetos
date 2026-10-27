import { useEffect, useState } from 'react'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router'
import { api, errorText } from '../api'
import { Field, Form } from '../form'
import type { BookmarkResponse, PageBookmarkResponse } from '../types/api'

// Bookmarks lists the user's bookmarks, the newest first, a page at a
// time (?page=): the current ones, with a form to add one, or the
// archived ones.
export function Bookmarks({ archived = false }: { archived?: boolean }) {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const page = Math.max(1, Number.parseInt(params.get('page') ?? '', 10) || 1)
  const [list, setList] = useState<PageBookmarkResponse>()
  const [failure, setFailure] = useState('')
  const [version, setVersion] = useState(0) // a change reloads the page
  const reload = () => setVersion((v) => v + 1)
  useEffect(() => {
    const ctl = new AbortController() // a newer request wins
    api.bookmarks.index({ archived, page, per_page: 10 }, { signal: ctl.signal }).then(
      (l) => {
        if (ctl.signal.aborted) return
        setList(l)
        setFailure('')
      },
      (err: unknown) => ctl.signal.aborted || setFailure(errorText(err)),
    )
    return () => ctl.abort()
  }, [archived, page, version])
  // Past the last page (its last bookmark deleted): the last page.
  if (list && list.data.length === 0 && page > 1 && list.last_page < page) {
    return <Navigate to={`?page=${Math.max(1, list.last_page)}`} replace />
  }
  return (
    <>
      <h1>{archived ? 'Archived' : 'Bookmarks'}</h1>
      {!archived && (
        <NewBookmark
          onCreate={() => {
            if (page > 1) navigate('?') // the newest are on the first page
            else reload()
          }}
        />
      )}
      {failure && (
        <p className="error" role="alert">
          {failure}
        </p>
      )}
      {list && list.data.length === 0 && <p className="muted">{archived ? 'Nothing archived.' : 'No bookmarks yet.'}</p>}
      <ul className="bookmarks">
        {list?.data.map((b) => <Bookmark key={b.id} bookmark={b} onChange={reload} onError={setFailure} />)}
      </ul>
      {list && list.last_page > 1 && (
        <nav className="pages" aria-label="Pages">
          {page > 1 && <Link to={`?page=${page - 1}`}>Newer</Link>}
          <span className="muted">
            Page {list.current_page} of {list.last_page}
          </span>
          {page < list.last_page && <Link to={`?page=${page + 1}`}>Older</Link>}
        </nav>
      )}
    </>
  )
}

function NewBookmark({ onCreate }: { onCreate: () => void }) {
  const [key, setKey] = useState(0) // a new key empties the form
  return (
    <details className="new" open>
      <summary>Add a bookmark</summary>
      <Form
        key={key}
        submit="Add"
        send={async (v) => {
          await api.bookmarks.create({ url: String(v.get('url')), title: String(v.get('title')), notes: String(v.get('notes') ?? '') })
          setKey((k) => k + 1)
          onCreate()
        }}
      >
        <Field name="url" label="Link" type="url" placeholder="https://" />
        <Field name="title" label="Title" />
        <Field name="notes" label="Notes" required={false} />
      </Form>
    </details>
  )
}

type BookmarkProps = { bookmark: BookmarkResponse; onChange: () => void; onError: (message: string) => void }

// Bookmark is one bookmark of the list, with its actions: edit, archive
// (or put back), delete.
function Bookmark({ bookmark: b, onChange, onError }: BookmarkProps) {
  const [editing, setEditing] = useState(false)
  if (editing) {
    return (
      <li>
        <Form
          submit="Save"
          send={async (v) => {
            await api.bookmarks.update({
              id: b.id,
              url: String(v.get('url')),
              title: String(v.get('title')),
              notes: String(v.get('notes') ?? ''),
              archived: b.archived,
            })
            setEditing(false)
            onChange()
          }}
        >
          <Field name="url" label="Link" type="url" defaultValue={b.url} />
          <Field name="title" label="Title" defaultValue={b.title} />
          <Field name="notes" label="Notes" defaultValue={b.notes} required={false} />
        </Form>
        <button type="button" className="link" onClick={() => setEditing(false)}>Cancel</button>
      </li>
    )
  }
  // act runs an action, then reloads the list, or shows why it failed.
  const act = (action: () => Promise<void>) => () =>
    action().then(onChange, (err: unknown) => onError(errorText(err)))
  const archive = act(() =>
    b.archived
      ? api.bookmarks.update({ id: b.id, url: b.url, title: b.title, notes: b.notes, archived: false }).then(() => {})
      : api.bookmarks.archive({ id: b.id }),
  )
  const remove = () => {
    if (confirm(`Delete “${b.title}”?`)) act(() => api.bookmarks.delete({ id: b.id }))()
  }
  return (
    <li>
      <a href={b.url} rel="noreferrer" target="_blank">{b.title}</a>
      <span className="muted url">{host(b.url)}</span>
      {b.notes && <p>{b.notes}</p>}
      <div className="actions">
        <button type="button" className="link" onClick={() => setEditing(true)}>Edit</button>
        <button type="button" className="link" onClick={archive}>{b.archived ? 'Put back' : 'Archive'}</button>
        <button type="button" className="link danger" onClick={remove}>Delete</button>
      </div>
    </li>
  )
}

// host is a link's host, or the link itself when the browser can't parse
// it (the API's url rule is Go's, which takes a few more).
function host(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return url
  }
}

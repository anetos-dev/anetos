import { Deferred, Link, router } from '@inertiajs/react'
import Layout from '../../Layout'
import type { PageProps } from '../../types/pages'

// Notes/Index: a page of notes, more appended on demand (a merge prop),
// and statistics loaded after the page shows (a deferred prop). Its props'
// types come from the Go types (go run . types).
export default function Index({ notes = [], next, stats }: PageProps<'Notes/Index'>) {
  return (
    <Layout>
      <h1>Notes</h1>
      <Deferred data="stats" fallback={<p>Counting…</p>}>
        <p id="stats">{stats?.count} notes, {stats?.words} words</p>
      </Deferred>
      <ul>
        {notes.map((n) => (
          <li key={n.id}>
            {n.title}{' '}
            <Link href={`/notes/${n.id}`} method="delete" as="button">Delete</Link>
          </li>
        ))}
      </ul>
      {next > 0 && (
        <button onClick={() => router.reload({ only: ['notes', 'next'], data: { page: next } })}>Load more</button>
      )}
    </Layout>
  )
}

import { Link, usePage } from '@inertiajs/react'
import type { ReactNode } from 'react'

// Layout is every page's frame: the app's name (a shared prop) and the
// message the server flashed (inertia.Flash).
export default function Layout({ children }: { children: ReactNode }) {
  const page = usePage<{ appName: string }>()
  const toast = page.flash?.toast as string | undefined
  return (
    <>
      <nav>
        <strong>{page.props.appName}</strong>
        <Link href="/notes">Notes</Link>
        <Link href="/notes/new">New note</Link>
        <Link href="/boom">An error</Link>
      </nav>
      {toast && <p className="toast" role="status">{toast}</p>}
      <main>{children}</main>
    </>
  )
}

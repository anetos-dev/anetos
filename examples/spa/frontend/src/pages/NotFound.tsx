import { Link } from 'react-router'

export function NotFound() {
  return (
    <>
      <h1>Page not found</h1>
      <p>
        <Link to="/">Go to your bookmarks</Link>
      </p>
    </>
  )
}

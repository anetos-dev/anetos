import Layout from '../Layout'
import type { ErrorProps } from '../types/pages'

// Error: the router's error pages (inertia.ErrorPages("Error")).
export default function Error({ status, title, detail }: ErrorProps) {
  return (
    <Layout>
      <h1>{status} {title}</h1>
      {detail && <p>{detail}</p>}
    </Layout>
  )
}

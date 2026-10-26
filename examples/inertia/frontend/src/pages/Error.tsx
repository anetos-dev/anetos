import Layout from '../Layout'

// Error: the router's error pages (inertia.ErrorPages("Error")).
export default function Error({ status, title, detail }: { status: number; title: string; detail: string }) {
  return (
    <Layout>
      <h1>{status} {title}</h1>
      {detail && <p>{detail}</p>}
    </Layout>
  )
}

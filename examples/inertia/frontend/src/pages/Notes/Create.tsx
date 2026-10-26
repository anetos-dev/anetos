import { Form } from '@inertiajs/react'
import Layout from '../../Layout'

// Notes/Create: the form posts to the server, which redirects back with
// its errors (in the errors prop) or to the list.
export default function Create({ maxTitle }: { maxTitle: number }) {
  return (
    <Layout>
      <h1>New note</h1>
      <Form action="/notes" method="post">
        {({ errors, processing }) => (
          <>
            <label htmlFor="title">Title (at most {maxTitle} characters)</label>
            <input id="title" name="title" />
            {errors.title && <p className="error">{errors.title}</p>}
            <label htmlFor="body">Body</label>
            <textarea id="body" name="body" rows={4} />
            {errors.body && <p className="error">{errors.body}</p>}
            <button type="submit" disabled={processing}>Save</button>
          </>
        )}
      </Form>
    </Layout>
  )
}

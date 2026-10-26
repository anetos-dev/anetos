// The frontend's entry: Inertia's client renders the page the server
// names (Notes/Index is pages/Notes/Index.tsx).
import { createInertiaApp } from '@inertiajs/react'
import { createRoot } from 'react-dom/client'
import type { ComponentType } from 'react'
import './app.css'

const pages = import.meta.glob<{ default: ComponentType }>('./pages/**/*.tsx')

createInertiaApp({
  resolve: (name) => {
    const page = pages[`./pages/${name}.tsx`]
    if (!page) throw new Error(`no page ${name}`)
    return page().then((m) => m.default)
  },
  setup({ el, App, props }) {
    if (el) createRoot(el).render(<App {...props} />)
  },
})

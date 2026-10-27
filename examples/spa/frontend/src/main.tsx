// The example's single-page app: React, with React Router for its pages,
// and the API's generated client (src/types/api.ts). The app serves its
// build at / (vite.SPA in main.go), index.html for every page's path; in
// development, Vite does (anetos dev runs it), and forwards /api to the
// app.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router'
import './app.css'
import { AuthProvider, GuestOnly, RequireUser } from './auth'
import { Layout } from './Layout'
import { Bookmarks } from './pages/Bookmarks'
import { ForgotPassword } from './pages/ForgotPassword'
import { Login } from './pages/Login'
import { NotFound } from './pages/NotFound'
import { Register } from './pages/Register'
import { ResetPassword } from './pages/ResetPassword'
import { VerifyEmail } from './pages/VerifyEmail'

// region: routes
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route element={<Layout />}>
            {/* Two keys: the lists don't share their state. */}
            <Route index element={<RequireUser><Bookmarks key="current" /></RequireUser>} />
            <Route path="archived" element={<RequireUser><Bookmarks key="archived" archived /></RequireUser>} />
            <Route path="login" element={<GuestOnly><Login /></GuestOnly>} />
            <Route path="register" element={<GuestOnly><Register /></GuestOnly>} />
            <Route path="forgot-password" element={<GuestOnly><ForgotPassword /></GuestOnly>} />
            {/* The account emails link to these two (AUTH_CLIENT_URL). */}
            <Route path="reset-password" element={<ResetPassword />} />
            <Route path="verify-email" element={<VerifyEmail />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  </StrictMode>,
)
// endregion

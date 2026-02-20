import { lazy, Suspense } from 'react'
import { Routes, Route } from 'react-router-dom'

// Lazy-load every page so only the current route's JS is parsed on FCP
const Home    = lazy(() => import('./pages/Home.jsx'))
const Post    = lazy(() => import('./pages/Post.jsx'))
const Login   = lazy(() => import('./pages/Login.jsx'))
const Profile = lazy(() => import('./pages/Profile.jsx'))
const NotFound = lazy(() => import('./pages/NotFound.jsx'))

export default function App() {
  return (
    <Suspense fallback={<div className="p-4 text-zinc-500">loading…</div>}>
      <Routes>
        <Route path="/"         element={<Home />} />
        <Route path="/post/:id" element={<Post />} />
        <Route path="/login"    element={<Login />} />
        <Route path="/user/:name" element={<Profile />} />
        <Route path="*"         element={<NotFound />} />
      </Routes>
    </Suspense>
  )
}

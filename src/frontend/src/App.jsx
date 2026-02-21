import { lazy, Suspense, useEffect } from "react";
import { Routes, Route } from "react-router-dom";
import useAuthStore from "./stores/authStore.js";
import Nav from "./components/Nav.jsx";
import RequireAdmin from "./components/RequireAdmin.jsx";

// Lazy-load every page so only the current route's JS is parsed on FCP
const Home = lazy(() => import("./pages/Home.jsx"));
const Post = lazy(() => import("./pages/Post.jsx"));
const Login = lazy(() => import("./pages/Login.jsx"));
const Profile = lazy(() => import("./pages/Profile.jsx"));
const NotFound = lazy(() => import("./pages/NotFound.jsx"));
const Admin = lazy(() => import("./pages/Admin.jsx"));

export default function App() {
  const hydrate = useAuthStore((s) => s.hydrate);
  useEffect(() => {
    hydrate();
  }, [hydrate]);
  return (
    <Suspense fallback={<div className="p-4 text-zinc-500">loading…</div>}>
      <Nav />
      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/post/:id" element={<Post />} />
        <Route path="/login" element={<Login />} />
        <Route path="/user/:name" element={<Profile />} />
        <Route
          path="/admin/*"
          element={
            <RequireAdmin>
              <Admin />
            </RequireAdmin>
          }
        />
        <Route path="*" element={<NotFound />} />
      </Routes>
    </Suspense>
  );
}

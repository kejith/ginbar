import { lazy, Suspense, useEffect } from "react";
import { Routes, Route } from "react-router-dom";
import useAuthStore from "./stores/authStore.js";
import Nav from "./components/Nav.jsx";
import RequireAdmin from "./components/RequireAdmin.jsx";

// Lazy-load every page so only the current route's JS is parsed on FCP
const Home = lazy(() => import("./pages/Home.jsx"));
const Login = lazy(() => import("./pages/Login.jsx"));
const Register = lazy(() => import("./pages/Register.jsx"));
const Profile = lazy(() => import("./pages/Profile.jsx"));
const UserGrid = lazy(() => import("./pages/UserGrid.jsx"));
const NotFound = lazy(() => import("./pages/NotFound.jsx"));
const Admin = lazy(() => import("./pages/Admin.jsx"));

export default function App() {
  const hydrate = useAuthStore((s) => s.hydrate);
  useEffect(() => {
    hydrate();
  }, [hydrate]);
  return (
    <Suspense
      fallback={<div className="p-4 text-(--color-muted)">loading…</div>}
    >
      <Nav />
      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/post/:postId" element={<Home />} />
        <Route path="/login" element={<Login />} />
        <Route path="/register" element={<Register />} />
        <Route path="/user/:name" element={<Profile />} />
        <Route path="/user/:name/posts" element={<UserGrid />} />
        <Route path="/user/:name/posts/:segment" element={<UserGrid />} />
        <Route path="/user/:name/posts/:tags/:postId" element={<UserGrid />} />
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

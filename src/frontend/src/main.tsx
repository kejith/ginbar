import { render } from "solid-js/web";
import App from "./App";
import Profile from "./Profile";
import { userIdFromPath } from "./profile-route.js";
import "./styles.css";

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");

const profileUserId = userIdFromPath(window.location.pathname);
render(() => profileUserId === null ? <App /> : <Profile userId={profileUserId} />, root);

import { render } from "solid-js/web";
import App from "./App";
import Messages from "./Messages";
import Profile from "./Profile";
import { messageRouteFromPath } from "./messages-state.js";
import { userIdFromPath } from "./profile-route.js";
import "./styles.css";

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");

const messageRoute = messageRouteFromPath(window.location.pathname);
const profileUserId = userIdFromPath(window.location.pathname);

render(
  () => messageRoute !== null || window.location.pathname.startsWith("/messages/")
    ? <Messages />
    : profileUserId === null
      ? <App />
      : <Profile userId={profileUserId} />,
  root,
);

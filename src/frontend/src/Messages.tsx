import {
  For,
  Show,
  createMemo,
  createSignal,
  onCleanup,
  onMount,
} from "solid-js";
import type { Component } from "solid-js";

import {
  APIError,
  fetchCurrentUser,
  fetchMessageInbox,
  fetchPrivateMessages,
  sendPrivateMessage,
  type CurrentUser,
  type MessageConversationSummary,
  type PrivateMessage,
} from "./api";
import {
  INBOX_PAGE_SIZE,
  MAX_MESSAGE_CHARACTERS,
  MAX_PENDING_SENDS,
  MAX_RETAINED_CONVERSATIONS,
  MAX_RETAINED_MESSAGES,
  MAX_RETAINED_THREADS,
  THREAD_PAGE_SIZE,
  clampMessageBody,
  mergeInboxConversations,
  mergeThreadMessages,
  messageBodyLength,
  messageRouteFromPath,
  messagesForDisplay,
  pathForMessagePeer,
  requestStillCurrent,
  touchThreadOrder,
} from "./messages-state.js";
import "./messages.css";

type AuthState =
  | { status: "loading" }
  | { status: "signed-out" }
  | { status: "signed-in"; user: CurrentUser }
  | { status: "unavailable" };

type MessagesRoute =
  | { kind: "inbox" }
  | { kind: "thread"; peerId: number };

interface ThreadState {
  messages: PrivateMessage[];
  nextBefore: number;
  loading: boolean;
  loadingOlder: boolean;
  error: string | null;
  unavailable: boolean;
  capped: boolean;
}

interface PendingSend {
  sequence: number;
  peerId: number;
  body: string;
}

interface SendError {
  peerId: number;
  message: string;
}

const emptyThreadState = (): ThreadState => ({
  messages: [],
  nextBefore: 0,
  loading: false,
  loadingOlder: false,
  error: null,
  unavailable: false,
  capped: false,
});

const Messages: Component = () => {
  let routeEpoch = 0;
  let sendSequence = 0;
  let inboxController: AbortController | undefined;
  let threadController: AbortController | undefined;

  const initialRoute = messageRouteFromPath(window.location.pathname);
  const [route, setRoute] = createSignal<MessagesRoute>(
    initialRoute ?? { kind: "inbox" },
  );
  const [invalidRoute, setInvalidRoute] = createSignal(initialRoute === null);
  const [authState, setAuthState] = createSignal<AuthState>({ status: "loading" });

  const [inbox, setInbox] = createSignal<MessageConversationSummary[]>([]);
  const [inboxNextBefore, setInboxNextBefore] = createSignal(0);
  const [inboxLoading, setInboxLoading] = createSignal(false);
  const [inboxLoadingOlder, setInboxLoadingOlder] = createSignal(false);
  const [inboxError, setInboxError] = createSignal<string | null>(null);
  const [inboxCapped, setInboxCapped] = createSignal(false);

  const [threads, setThreads] = createSignal<ReadonlyMap<number, ThreadState>>(new Map());
  const [threadOrder, setThreadOrder] = createSignal<number[]>([]);
  const [draft, setDraft] = createSignal("");
  const [pendingSends, setPendingSends] = createSignal<PendingSend[]>([]);
  const [sendError, setSendError] = createSignal<SendError | null>(null);

  const currentPeerID = createMemo(() => {
    const value = route();
    return value.kind === "thread" ? value.peerId : null;
  });

  const currentThread = createMemo(() => {
    const peerID = currentPeerID();
    return peerID === null ? null : (threads().get(peerID) ?? emptyThreadState());
  });

  const currentPeerSummary = createMemo(() => {
    const peerID = currentPeerID();
    if (peerID === null) return null;
    return inbox().find((conversation) => conversation.peer.id === peerID) ?? null;
  });

  const currentPeerLabel = createMemo(() => {
    const peerID = currentPeerID();
    if (peerID === null) return "";
    const peer = currentPeerSummary()?.peer;
    if (peer?.available && peer.username) return peer.username;
    return `User #${peerID}`;
  });

  const currentPending = createMemo(() => {
    const peerID = currentPeerID();
    if (peerID === null) return [];
    return pendingSends()
      .filter((item) => item.peerId === peerID)
      .sort((a, b) => a.sequence - b.sequence);
  });

  const currentMessages = createMemo(() => {
    const thread = currentThread();
    return thread === null ? [] : messagesForDisplay(thread.messages);
  });

  const currentSendError = createMemo(() => {
    const error = sendError();
    return error !== null && error.peerId === currentPeerID() ? error.message : null;
  });

  const updateThread = (peerID: number, update: (current: ThreadState) => ThreadState) => {
    setThreads((previous) => {
      const next = new Map(previous);
      next.set(peerID, update(next.get(peerID) ?? emptyThreadState()));
      return next;
    });
  };

  const retainThread = (peerID: number) => {
    setThreadOrder((previous) => {
      const nextOrder = touchThreadOrder(previous, peerID, MAX_RETAINED_THREADS);
      const retained = new Set(nextOrder);
      setThreads((current) => {
        let changed = false;
        const next = new Map<number, ThreadState>();
        for (const [id, value] of current) {
          if (retained.has(id)) next.set(id, value);
          else changed = true;
        }
        return changed ? next : current;
      });
      return nextOrder;
    });
  };

  const markUnauthenticated = () => {
    setAuthState({ status: "signed-out" });
    inboxController?.abort();
    threadController?.abort();
  };

  const loadInbox = async (before: number, replace: boolean, epoch: number) => {
    inboxController?.abort();
    const controller = new AbortController();
    inboxController = controller;
    if (replace) setInboxLoading(true);
    else setInboxLoadingOlder(true);
    setInboxError(null);

    try {
      const page = await fetchMessageInbox(before, INBOX_PAGE_SIZE, controller.signal);
      if (
        controller.signal.aborted
        || !requestStillCurrent(null, epoch, currentPeerID(), routeEpoch)
      ) return;

      const merged = mergeInboxConversations(
        replace ? [] : inbox(),
        page.conversations,
        false,
        MAX_RETAINED_CONVERSATIONS,
      );
      const capped = merged.length >= MAX_RETAINED_CONVERSATIONS && Boolean(page.nextBefore);
      setInbox(merged);
      setInboxNextBefore(capped ? 0 : (page.nextBefore ?? 0));
      setInboxCapped(capped);
    } catch (error) {
      if (
        controller.signal.aborted
        || !requestStillCurrent(null, epoch, currentPeerID(), routeEpoch)
      ) return;
      if (error instanceof APIError && error.status === 401) {
        markUnauthenticated();
        return;
      }
      setInboxError("Messages could not be loaded.");
    } finally {
      if (inboxController === controller) inboxController = undefined;
      if (
        !controller.signal.aborted
        && requestStillCurrent(null, epoch, currentPeerID(), routeEpoch)
      ) {
        if (replace) setInboxLoading(false);
        else setInboxLoadingOlder(false);
      }
    }
  };

  const loadThread = async (
    peerID: number,
    before: number,
    replace: boolean,
    epoch: number,
  ) => {
    threadController?.abort();
    const controller = new AbortController();
    threadController = controller;
    retainThread(peerID);
    updateThread(peerID, (current) => ({
      ...current,
      loading: replace,
      loadingOlder: !replace,
      error: null,
      unavailable: replace ? false : current.unavailable,
      capped: replace ? false : current.capped,
    }));

    try {
      const page = await fetchPrivateMessages(peerID, before, THREAD_PAGE_SIZE, controller.signal);
      if (
        controller.signal.aborted
        || !requestStillCurrent(peerID, epoch, currentPeerID(), routeEpoch)
      ) return;

      updateThread(peerID, (current) => {
        const merged = mergeThreadMessages(
          replace ? [] : current.messages,
          page.messages,
          false,
          MAX_RETAINED_MESSAGES,
        );
        const capped = merged.length >= MAX_RETAINED_MESSAGES && Boolean(page.nextBefore);
        return {
          ...current,
          messages: merged,
          nextBefore: capped ? 0 : (page.nextBefore ?? 0),
          loading: false,
          loadingOlder: false,
          error: null,
          unavailable: false,
          capped,
        };
      });
    } catch (error) {
      if (
        controller.signal.aborted
        || !requestStillCurrent(peerID, epoch, currentPeerID(), routeEpoch)
      ) return;
      if (error instanceof APIError && error.status === 401) {
        markUnauthenticated();
        return;
      }
      const unavailable = error instanceof APIError
        && error.status === 404
        && error.code === "recipient_unavailable";
      updateThread(peerID, (current) => ({
        ...current,
        loading: false,
        loadingOlder: false,
        error: unavailable ? null : "Conversation could not be loaded.",
        unavailable: unavailable || current.unavailable,
      }));
    } finally {
      if (threadController === controller) threadController = undefined;
    }
  };

  const syncRoute = () => {
    const next = messageRouteFromPath(window.location.pathname);
    routeEpoch += 1;
    inboxController?.abort();
    threadController?.abort();
    setDraft("");
    setSendError(null);

    if (next === null) {
      setInvalidRoute(true);
      setRoute({ kind: "inbox" });
      document.title = "Messages · Ginbar";
      return;
    }

    setInvalidRoute(false);
    setRoute(next);
    const epoch = routeEpoch;
    if (next.kind === "inbox") {
      document.title = "Messages · Ginbar";
      if (authState().status === "signed-in") void loadInbox(0, true, epoch);
      return;
    }

    retainThread(next.peerId);
    document.title = `Conversation #${next.peerId} · Ginbar`;
    if (authState().status === "signed-in") {
      void loadThread(next.peerId, 0, true, epoch);
    }
  };

  const navigateInbox = () => {
    if (window.location.pathname === "/messages") return;
    history.pushState({}, "", "/messages");
    syncRoute();
  };

  const navigateThread = (peerID: number) => {
    const path = pathForMessagePeer(peerID);
    if (window.location.pathname === path) return;
    history.pushState({ peerId: peerID }, "", path);
    syncRoute();
  };

  const refreshCurrent = () => {
    const state = authState();
    if (state.status !== "signed-in" || invalidRoute()) return;
    const currentRoute = route();
    if (currentRoute.kind === "inbox") {
      void loadInbox(0, true, routeEpoch);
    } else {
      void loadThread(currentRoute.peerId, 0, true, routeEpoch);
    }
  };

  const loadOlderInbox = () => {
    const before = inboxNextBefore();
    if (before <= 0 || inboxLoadingOlder() || inboxCapped()) return;
    void loadInbox(before, false, routeEpoch);
  };

  const loadOlderThread = () => {
    const peerID = currentPeerID();
    const thread = currentThread();
    if (
      peerID === null
      || thread === null
      || thread.nextBefore <= 0
      || thread.loadingOlder
      || thread.capped
    ) return;
    void loadThread(peerID, thread.nextBefore, false, routeEpoch);
  };

  const updateExistingInboxAfterSend = (peerID: number, message: PrivateMessage) => {
    setInbox((current) => {
      const existing = current.find((conversation) => conversation.peer.id === peerID);
      if (!existing) return current;
      return mergeInboxConversations(
        current,
        [{
          ...existing,
          latestMessage: {
            id: message.id,
            senderId: message.senderId,
            createdAt: message.createdAt,
          },
        }],
        false,
        MAX_RETAINED_CONVERSATIONS,
      );
    });
  };

  const markPeerUnavailable = (peerID: number) => {
    updateThread(peerID, (current) => ({ ...current, unavailable: true }));
    setInbox((current) => current.map((conversation) => (
      conversation.peer.id === peerID
        ? { ...conversation, peer: { id: peerID, available: false } }
        : conversation
    )));
  };

  const submitMessage = async () => {
    const auth = authState();
    const peerID = currentPeerID();
    const thread = currentThread();
    const body = draft();
    if (
      auth.status !== "signed-in"
      || peerID === null
      || thread === null
      || thread.unavailable
      || messageBodyLength(body) < 1
      || messageBodyLength(body) > MAX_MESSAGE_CHARACTERS
      || pendingSends().length >= MAX_PENDING_SENDS
    ) return;

    const sequence = ++sendSequence;
    const pending: PendingSend = { sequence, peerId: peerID, body };
    setDraft("");
    setSendError(null);
    setPendingSends((current) => [...current, pending]);

    try {
      const created = await sendPrivateMessage(peerID, body);
      setPendingSends((current) => current.filter((item) => item.sequence !== sequence));
      if (threads().has(peerID)) {
        updateThread(peerID, (current) => ({
          ...current,
          messages: mergeThreadMessages(
            current.messages,
            [created],
            false,
            MAX_RETAINED_MESSAGES,
          ),
        }));
      }
      updateExistingInboxAfterSend(peerID, created);
    } catch (error) {
      const newerPending = pendingSends().some(
        (item) => item.peerId === peerID && item.sequence > sequence,
      );
      setPendingSends((current) => current.filter((item) => item.sequence !== sequence));

      if (error instanceof APIError && error.status === 401) {
        markUnauthenticated();
      } else if (
        error instanceof APIError
        && error.status === 404
        && error.code === "recipient_unavailable"
      ) {
        markPeerUnavailable(peerID);
      }

      if (currentPeerID() === peerID) {
        if (!newerPending && draft() === "") setDraft(body);
        const message = error instanceof APIError && error.code === "recipient_unavailable"
          ? "This user is no longer available."
          : error instanceof APIError && error.status === 401
            ? "Authentication required."
            : "Message could not be sent.";
        setSendError({ peerId: peerID, message });
      }
    }
  };

  onMount(() => {
    const authController = new AbortController();
    const onPopState = () => syncRoute();
    window.addEventListener("popstate", onPopState);

    void fetchCurrentUser(authController.signal)
      .then((user) => {
        if (authController.signal.aborted) return;
        if (user === null) {
          setAuthState({ status: "signed-out" });
          return;
        }
        setAuthState({ status: "signed-in", user });
        syncRoute();
      })
      .catch(() => {
        if (!authController.signal.aborted) setAuthState({ status: "unavailable" });
      });

    onCleanup(() => {
      routeEpoch += 1;
      authController.abort();
      inboxController?.abort();
      threadController?.abort();
      window.removeEventListener("popstate", onPopState);
    });
  });

  return (
    <main class="messages-page">
      <header class="messages-topbar">
        <div>
          <a href="/">Ginbar v2</a>
          <a href="/">Board</a>
          <Show when={!invalidRoute() && route().kind === "thread"}>
            <button type="button" class="messages-link-button" onClick={navigateInbox}>Inbox</button>
          </Show>
        </div>
        <Show when={authState().status === "signed-in"}>
          <span>{(authState() as { status: "signed-in"; user: CurrentUser }).user.username}</span>
        </Show>
      </header>

      <Show when={invalidRoute()}>
        <section class="messages-state" role="status">
          <strong>Invalid message route</strong>
          <p>Conversation routes use a numeric user ID.</p>
          <button type="button" onClick={navigateInbox}>Open inbox</button>
        </section>
      </Show>

      <Show when={!invalidRoute() && authState().status === "loading"}>
        <p class="messages-state" role="status">Checking session…</p>
      </Show>
      <Show when={!invalidRoute() && authState().status === "signed-out"}>
        <section class="messages-state" role="status">
          <strong>Authentication required</strong>
          <p>Messages are available only to signed-in users.</p>
          <a href="/">Return to board</a>
        </section>
      </Show>
      <Show when={!invalidRoute() && authState().status === "unavailable"}>
        <section class="messages-state" role="alert">
          <strong>Session could not be checked</strong>
          <button type="button" onClick={() => window.location.reload()}>Retry</button>
        </section>
      </Show>

      <Show when={!invalidRoute() && authState().status === "signed-in" && route().kind === "inbox"}>
        <section class="messages-shell" aria-label="Message inbox">
          <div class="messages-heading">
            <div>
              <h1>Messages</h1>
              <span>{inbox().length.toLocaleString()} conversations retained</span>
            </div>
            <button type="button" disabled={inboxLoading()} onClick={refreshCurrent}>
              {inboxLoading() ? "Refreshing…" : "Refresh"}
            </button>
          </div>

          <Show when={inboxError()}>
            {(message) => <p class="messages-error" role="alert">{message()}</p>}
          </Show>
          <Show when={inboxLoading() && inbox().length === 0}>
            <p class="messages-state" role="status">Loading messages…</p>
          </Show>
          <Show when={!inboxLoading() && inbox().length === 0 && inboxError() === null}>
            <p class="messages-empty">No conversations yet.</p>
          </Show>

          <div class="conversation-list">
            <For each={inbox()}>
              {(conversation) => (
                <Show
                  when={conversation.peer.available}
                  fallback={
                    <article class="conversation-row conversation-row--unavailable">
                      <div>
                        <strong>Unavailable user #{conversation.peer.id}</strong>
                        <span>This account is unavailable.</span>
                      </div>
                      <ConversationMeta
                        conversation={conversation}
                        currentUserID={(authState() as { status: "signed-in"; user: CurrentUser }).user.id}
                      />
                    </article>
                  }
                >
                  <button
                    type="button"
                    class="conversation-row conversation-row--button"
                    data-peer-id={conversation.peer.id}
                    onClick={() => navigateThread(conversation.peer.id)}
                  >
                    <div>
                      <strong>{conversation.peer.username ?? `User #${conversation.peer.id}`}</strong>
                      <span>user #{conversation.peer.id}</span>
                    </div>
                    <ConversationMeta
                      conversation={conversation}
                      currentUserID={(authState() as { status: "signed-in"; user: CurrentUser }).user.id}
                    />
                  </button>
                </Show>
              )}
            </For>
          </div>

          <Show when={inboxNextBefore() > 0 && !inboxCapped()}>
            <button
              type="button"
              class="messages-load-more"
              disabled={inboxLoadingOlder()}
              onClick={loadOlderInbox}
            >
              {inboxLoadingOlder() ? "Loading…" : "Load older conversations"}
            </button>
          </Show>
          <Show when={inboxCapped()}>
            <p class="messages-retention-note">
              Retention limit reached. Refresh to return to the newest conversations.
            </p>
          </Show>
        </section>
      </Show>

      <Show when={!invalidRoute() && authState().status === "signed-in" && route().kind === "thread"}>
        <section class="messages-shell messages-thread" aria-label="Direct conversation">
          <div class="messages-heading">
            <div>
              <h1>{currentPeerLabel()}</h1>
              <span>user #{currentPeerID()}</span>
            </div>
            <button
              type="button"
              disabled={currentThread()?.loading ?? false}
              onClick={refreshCurrent}
            >
              {(currentThread()?.loading ?? false) ? "Refreshing…" : "Refresh"}
            </button>
          </div>

          <Show when={currentThread()?.unavailable}>
            <section class="messages-state messages-state--compact" role="status">
              <strong>User unavailable</strong>
              <p>This conversation cannot currently be opened or sent to.</p>
            </section>
          </Show>

          <Show when={currentThread()?.error}>
            {(message) => <p class="messages-error" role="alert">{message()}</p>}
          </Show>

          <Show when={(currentThread()?.nextBefore ?? 0) > 0 && !(currentThread()?.capped ?? false)}>
            <button
              type="button"
              class="messages-load-more"
              disabled={currentThread()?.loadingOlder ?? false}
              onClick={loadOlderThread}
            >
              {(currentThread()?.loadingOlder ?? false) ? "Loading…" : "Load older messages"}
            </button>
          </Show>
          <Show when={currentThread()?.capped}>
            <p class="messages-retention-note">
              Retention limit reached. Refresh to return to the newest messages.
            </p>
          </Show>

          <Show when={(currentThread()?.loading ?? false) && currentMessages().length === 0}>
            <p class="messages-state" role="status">Loading conversation…</p>
          </Show>
          <Show
            when={
              !(currentThread()?.loading ?? false)
              && !(currentThread()?.unavailable ?? false)
              && currentMessages().length === 0
              && currentPending().length === 0
              && currentThread()?.error === null
            }
          >
            <p class="messages-empty">No messages yet.</p>
          </Show>

          <div class="message-list" aria-live="polite">
            <For each={currentMessages()}>
              {(message) => (
                <MessageRow
                  message={message}
                  currentUserID={(authState() as { status: "signed-in"; user: CurrentUser }).user.id}
                />
              )}
            </For>
            <For each={currentPending()}>
              {(pending) => (
                <article class="message-row message-row--outgoing message-row--pending">
                  <p>{pending.body}</p>
                  <span>Sending…</span>
                </article>
              )}
            </For>
          </div>

          <Show when={currentSendError()}>
            {(message) => <p class="messages-error" role="alert">{message()}</p>}
          </Show>

          <form
            class="message-composer"
            onSubmit={(event) => {
              event.preventDefault();
              void submitMessage();
            }}
          >
            <label for="message-body">Message</label>
            <textarea
              id="message-body"
              rows={4}
              value={draft()}
              disabled={currentThread()?.unavailable ?? false}
              placeholder={currentThread()?.unavailable ? "User unavailable" : "Write a message"}
              onInput={(event) => setDraft(clampMessageBody(event.currentTarget.value))}
            />
            <div>
              <span>{messageBodyLength(draft()).toLocaleString()} / {MAX_MESSAGE_CHARACTERS.toLocaleString()}</span>
              <button
                type="submit"
                disabled={
                  (currentThread()?.unavailable ?? false)
                  || messageBodyLength(draft()) < 1
                  || pendingSends().length >= MAX_PENDING_SENDS
                }
              >
                Send
              </button>
            </div>
            <Show when={pendingSends().length >= MAX_PENDING_SENDS}>
              <p class="messages-retention-note">Wait for a pending message to finish before sending another.</p>
            </Show>
          </form>
        </section>
      </Show>
    </main>
  );
};

const ConversationMeta: Component<{
  conversation: MessageConversationSummary;
  currentUserID: number;
}> = (props) => (
  <div class="conversation-meta">
    <span>
      {props.conversation.latestMessage.senderId === props.currentUserID ? "You" : "Peer"}
      {" · "}message #{props.conversation.latestMessage.id}
    </span>
    <time dateTime={props.conversation.latestMessage.createdAt}>
      {formatTimestamp(props.conversation.latestMessage.createdAt)}
    </time>
  </div>
);

const MessageRow: Component<{
  message: PrivateMessage;
  currentUserID: number;
}> = (props) => (
  <article
    class="message-row"
    classList={{ "message-row--outgoing": props.message.senderId === props.currentUserID }}
    data-message-id={props.message.id}
  >
    <p>{props.message.body}</p>
    <span>
      #{props.message.id}
      {" · "}
      <time dateTime={props.message.createdAt}>{formatTimestamp(props.message.createdAt)}</time>
    </span>
  </article>
);

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}

export default Messages;

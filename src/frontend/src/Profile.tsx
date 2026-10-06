import { For, Show, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import type { Component } from "solid-js";

import { APIError, type PostSummary } from "./api";
import { mediaPath, thumbnailStorageKey } from "./board-data.js";
import { fetchProfile, type PublicProfileUser } from "./profile-api";
import "./profile.css";

const PROFILE_PAGE_SIZE = 33;

type ProfileStatus = "loading" | "ready" | "not-found" | "error";

const Profile: Component<{ userId: number }> = (props) => {
  let requestSequence = 0;
  let controller: AbortController | undefined;

  const [user, setUser] = createSignal<PublicProfileUser | null>(null);
  const [posts, setPosts] = createSignal<PostSummary[]>([]);
  const [nextBefore, setNextBefore] = createSignal(0);
  const [status, setStatus] = createSignal<ProfileStatus>("loading");
  const [loadingMore, setLoadingMore] = createSignal(false);
  const [loadMoreError, setLoadMoreError] = createSignal(false);

  const memberSince = createMemo(() => {
    const value = user()?.createdAt;
    if (!value) return "";
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return "";
    return new Intl.DateTimeFormat(undefined, { year: "numeric", month: "long", day: "numeric" }).format(date);
  });

  const load = async (before: number, replace: boolean) => {
    const sequence = ++requestSequence;
    controller?.abort();
    const nextController = new AbortController();
    controller = nextController;
    if (replace) setStatus("loading");
    else setLoadingMore(true);
    setLoadMoreError(false);

    try {
      const page = await fetchProfile(props.userId, before, PROFILE_PAGE_SIZE, nextController.signal);
      if (nextController.signal.aborted || sequence !== requestSequence) return;
      setUser(page.user);
      setPosts((current) => replace ? page.posts : mergeProfilePosts(current, page.posts));
      setNextBefore(page.nextBefore ?? 0);
      setStatus("ready");
      document.title = `${page.user.username} · Ginbar`;
    } catch (error) {
      if (nextController.signal.aborted || sequence !== requestSequence) return;
      if (replace) {
        setStatus(error instanceof APIError && error.status === 404 ? "not-found" : "error");
      } else {
        setLoadMoreError(true);
      }
    } finally {
      if (controller === nextController) controller = undefined;
      if (!nextController.signal.aborted && sequence === requestSequence) setLoadingMore(false);
    }
  };

  onMount(() => {
    void load(0, true);
    onCleanup(() => {
      requestSequence += 1;
      controller?.abort();
    });
  });

  return (
    <main class="profile-page">
      <header class="profile-topbar">
        <a href="/">Ginbar v2</a>
        <a href="/">Board</a>
      </header>

      <Show when={status() === "loading"}>
        <p class="profile-state" role="status">Loading profile…</p>
      </Show>
      <Show when={status() === "not-found"}>
        <section class="profile-state" role="status">
          <strong>User not found</strong>
          <p>This profile is unavailable.</p>
        </section>
      </Show>
      <Show when={status() === "error"}>
        <section class="profile-state" role="alert">
          <strong>Profile could not be loaded</strong>
          <button type="button" onClick={() => void load(0, true)}>Retry</button>
        </section>
      </Show>

      <Show when={status() === "ready" && user() !== null}>
        <section class="profile-hero">
          <div>
            <h1>{user()!.username}</h1>
            <span>user #{user()!.id}</span>
          </div>
          <Show when={memberSince() !== ""}>
            <span>Member since {memberSince()}</span>
          </Show>
        </section>

        <section class="profile-posts" aria-label={`Latest posts by ${user()!.username}`}>
          <div class="profile-section-heading">
            <strong>Latest posts</strong>
            <span>{posts().length.toLocaleString()} loaded</span>
          </div>
          <Show when={posts().length === 0}>
            <p class="profile-empty">No public posts yet.</p>
          </Show>
          <div class="profile-grid">
            <For each={posts()}>
              {(post) => (
                <a class="profile-thumbnail" href={`/post/${post.id}`} aria-label={`Open post ${post.id}`}>
                  <Show when={safeThumbnailURL(post)} fallback={<span class="profile-thumbnail-placeholder" aria-hidden="true" />}>
                    {(url) => <img src={url()} width="256" height="256" alt="" loading="lazy" decoding="async" />}
                  </Show>
                  <span class="profile-thumbnail-id">#{post.id}</span>
                  <span class="profile-thumbnail-score">{post.score}</span>
                </a>
              )}
            </For>
          </div>
          <Show when={nextBefore() > 0}>
            <button
              class="profile-load-more"
              type="button"
              disabled={loadingMore()}
              onClick={() => void load(nextBefore(), false)}
            >
              {loadingMore() ? "Loading…" : "Load more"}
            </button>
          </Show>
          <Show when={loadMoreError()}>
            <p class="profile-load-error" role="alert">More posts could not be loaded.</p>
          </Show>
        </section>
      </Show>
    </main>
  );
};

function safeThumbnailURL(post: PostSummary): string | null {
  try {
    return mediaPath(thumbnailStorageKey(post.media.storageKey));
  } catch {
    return null;
  }
}

function mergeProfilePosts(current: PostSummary[], incoming: PostSummary[]): PostSummary[] {
  const byID = new Map<number, PostSummary>();
  for (const post of current) byID.set(post.id, post);
  for (const post of incoming) if (!byID.has(post.id)) byID.set(post.id, post);
  return [...byID.values()].sort((a, b) => b.id - a.id);
}

export default Profile;

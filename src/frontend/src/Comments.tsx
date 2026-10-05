import { For, Show, createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import type { Accessor, Component } from "solid-js";

import { APIError, createComment, fetchComments, type Comment } from "./api";
import { buildCommentRows, mergeCommentsByID } from "./comment-tree.js";
import "./comments.css";

interface CommentsProps {
  postId: number;
  canCreate: Accessor<boolean>;
}

const Comments: Component<CommentsProps> = (props) => {
  let composer!: HTMLTextAreaElement;
  let readController: AbortController | undefined;
  let createController: AbortController | undefined;
  let epoch = 0;

  const [comments, setComments] = createSignal<Comment[]>([]);
  const [nextAfter, setNextAfter] = createSignal(0);
  const [loading, setLoading] = createSignal(false);
  const [loadError, setLoadError] = createSignal<string | null>(null);
  const [draft, setDraft] = createSignal("");
  const [replyTo, setReplyTo] = createSignal<number | null>(null);
  const [submitting, setSubmitting] = createSignal(false);
  const [submitError, setSubmitError] = createSignal<string | null>(null);
  const [authBlocked, setAuthBlocked] = createSignal(false);

  const canCreate = createMemo(() => props.canCreate() && !authBlocked());
  const rows = createMemo(() => buildCommentRows(comments()));

  const loadPage = async (postId: number, after: number, replace: boolean, requestEpoch: number) => {
    readController?.abort();
    const controller = new AbortController();
    readController = controller;
    setLoading(true);
    setLoadError(null);
    try {
      const page = await fetchComments(postId, after, 100, controller.signal);
      if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
      setComments((current) => replace ? page.comments : mergeCommentsByID(current, page.comments));
      setNextAfter(page.nextAfter ?? 0);
    } catch (error) {
      if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
      if (error instanceof APIError && error.status === 404) setLoadError("Comments are unavailable for this post");
      else setLoadError("Comments could not be loaded");
    } finally {
      if (readController === controller) readController = undefined;
      if (!controller.signal.aborted && requestEpoch === epoch && props.postId === postId) setLoading(false);
    }
  };

  createEffect(() => {
    const postId = props.postId;
    const requestEpoch = ++epoch;
    readController?.abort();
    createController?.abort();
    setComments([]);
    setNextAfter(0);
    setLoading(false);
    setLoadError(null);
    setDraft("");
    setReplyTo(null);
    setSubmitting(false);
    setSubmitError(null);
    setAuthBlocked(false);
    void loadPage(postId, 0, true, requestEpoch);

    onCleanup(() => {
      readController?.abort();
      createController?.abort();
    });
  });

  const beginReply = (commentId: number) => {
    setReplyTo(commentId);
    setSubmitError(null);
    queueMicrotask(() => composer?.focus());
  };

  const submit = async (event: SubmitEvent) => {
    event.preventDefault();
    if (!canCreate() || submitting()) return;

    const postId = props.postId;
    const requestEpoch = epoch;
    const body = draft();
    const parentCommentId = replyTo() ?? undefined;
    createController?.abort();
    const controller = new AbortController();
    createController = controller;
    setSubmitting(true);
    setSubmitError(null);
    try {
      const created = await createComment(postId, body, parentCommentId, controller.signal);
      if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
      setComments((current) => mergeCommentsByID(current, [created]));
      setDraft("");
      setReplyTo(null);
    } catch (error) {
      if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
      if (error instanceof APIError && error.status === 401) {
        setAuthBlocked(true);
        setSubmitError("Authentication required");
      } else if (error instanceof APIError) {
        setSubmitError(error.message);
      } else {
        setSubmitError("Comment could not be posted");
      }
    } finally {
      if (createController === controller) createController = undefined;
      if (!controller.signal.aborted && requestEpoch === epoch && props.postId === postId) setSubmitting(false);
    }
  };

  return (
    <section class="comments" aria-label={`Comments for post ${props.postId}`}>
      <div class="comments-header">
        <strong>Comments</strong>
        <span>{comments().length.toLocaleString()} loaded</span>
      </div>

      <Show when={canCreate()} fallback={<p class="comments-auth-note">Sign in to comment or reply.</p>}>
        <form class="comment-composer" onSubmit={submit}>
          <Show when={replyTo()}>
            {(parentId) => (
              <div class="comment-reply-target">
                <span>Replying to #{parentId()}</span>
                <button type="button" onClick={() => setReplyTo(null)}>Cancel reply</button>
              </div>
            )}
          </Show>
          <textarea
            ref={composer}
            value={draft()}
            rows={3}
            aria-label={replyTo() === null ? "New comment" : `Reply to comment ${replyTo()}`}
            placeholder={replyTo() === null ? "Write a comment" : "Write a reply"}
            disabled={submitting()}
            onInput={(event) => setDraft(event.currentTarget.value)}
          />
          <div class="comment-composer-actions">
            <span>{Array.from(draft()).length.toLocaleString()} / 10,000 characters</span>
            <button type="submit" disabled={submitting() || draft().length === 0}>
              {submitting() ? "Posting…" : replyTo() === null ? "Post comment" : "Post reply"}
            </button>
          </div>
          <Show when={submitError()}>
            {(message) => <p class="comment-error" role="alert">{message()}</p>}
          </Show>
        </form>
      </Show>

      <Show when={loadError()}>
        {(message) => <p class="comment-error" role="alert">{message()}</p>}
      </Show>
      <Show when={loading() && comments().length === 0}>
        <p class="comments-status" role="status">Loading comments…</p>
      </Show>
      <Show when={!loading() && loadError() === null && comments().length === 0}>
        <p class="comments-status">No comments yet.</p>
      </Show>

      <div class="comment-tree" role="tree" aria-label="Comment thread">
        <For each={rows()}>
          {(row) => (
            <article
              class="comment"
              classList={{ "comment--orphaned": row.orphaned, "comment--deleted": row.comment.deleted }}
              style={`--comment-depth: ${Math.min(row.depth, 32)}`}
              data-comment-id={row.comment.id}
              data-parent-comment-id={row.comment.parentCommentId ?? undefined}
              data-depth={row.depth}
              role="treeitem"
              aria-level={row.depth + 1}
            >
              <div class="comment-meta">
                <strong>#{row.comment.id}</strong>
                <span>user {row.comment.authorId}</span>
                <span>{row.comment.score} points</span>
              </div>
              <Show when={row.orphaned}>
                <p class="comment-orphan-warning">Parent comment unavailable</p>
              </Show>
              <p class="comment-body">{row.comment.deleted ? "[deleted]" : row.comment.body}</p>
              <Show when={canCreate() && !row.comment.deleted && !row.orphaned}>
                <button type="button" class="comment-reply" onClick={() => beginReply(row.comment.id)}>Reply</button>
              </Show>
            </article>
          )}
        </For>
      </div>

      <Show when={nextAfter() > 0}>
        <button
          type="button"
          class="comments-load-more"
          disabled={loading()}
          onClick={() => void loadPage(props.postId, nextAfter(), false, epoch)}
        >
          {loading() ? "Loading…" : "Load more comments"}
        </button>
      </Show>
    </section>
  );
};

export default Comments;

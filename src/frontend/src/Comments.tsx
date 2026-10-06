import { For, Show, createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import type { Accessor, Component } from "solid-js";

import {
  APIError,
  createComment,
  fetchComments,
  hideComment,
  hidePost,
  setCommentVote,
  type Comment,
  type PostVote,
} from "./api";
import { buildCommentRows, mergeCommentsByID } from "./comment-tree.js";
import { nextCommentVoteForDirection, withOptimisticCommentVote } from "./comment-vote.js";
import { canApplyModerationResult, withModeratedComment } from "./moderation-state.js";
import "./comments.css";

interface CommentsProps {
  postId: number;
  canCreate: Accessor<boolean>;
  onPostModerated(postId: number): void;
}

interface ConfirmedVoteState {
  score: number;
  vote: PostVote;
}

interface VoteError {
  commentId: number;
  message: string;
}

type CommentRow = ReturnType<typeof buildCommentRows<Comment>>[number];

const Comments: Component<CommentsProps> = (props) => {
  let composer!: HTMLTextAreaElement;
  let readController: AbortController | undefined;
  let createController: AbortController | undefined;
  let moderationController: AbortController | undefined;
  let epoch = 0;
  const voteQueues = new Map<number, Promise<void>>();
  const voteSequences = new Map<number, number>();
  const confirmedVotes = new Map<number, ConfirmedVoteState>();
  const voteControllers = new Map<number, AbortController>();
  const rowCache = new Map<number, CommentRow>();

  const [comments, setComments] = createSignal<Comment[]>([]);
  const [nextAfter, setNextAfter] = createSignal(0);
  const [loading, setLoading] = createSignal(false);
  const [loadError, setLoadError] = createSignal<string | null>(null);
  const [draft, setDraft] = createSignal("");
  const [replyTo, setReplyTo] = createSignal<number | null>(null);
  const [submitting, setSubmitting] = createSignal(false);
  const [submitError, setSubmitError] = createSignal<string | null>(null);
  const [voteError, setVoteError] = createSignal<VoteError | null>(null);
  const [authBlocked, setAuthBlocked] = createSignal(false);
  const [canModerate, setCanModerate] = createSignal(false);
  const [moderatingTarget, setModeratingTarget] = createSignal<"post" | number | null>(null);
  const [moderationError, setModerationError] = createSignal<string | null>(null);

  const canCreate = createMemo(() => props.canCreate() && !authBlocked());
  const rows = createMemo(() => {
    const nextRows = buildCommentRows(comments());
    const liveIDs = new Set<number>();
    const stableRows = nextRows.map((row) => {
      liveIDs.add(row.comment.id);
      const cached = rowCache.get(row.comment.id);
      if (
        cached
        && cached.comment === row.comment
        && cached.depth === row.depth
        && cached.orphaned === row.orphaned
      ) return cached;
      rowCache.set(row.comment.id, row);
      return row;
    });
    for (const id of rowCache.keys()) {
      if (!liveIDs.has(id)) rowCache.delete(id);
    }
    return stableRows;
  });

  const updateCommentByID = (commentId: number, update: (comment: Comment) => Comment) => {
    setComments((current) => {
      const index = current.findIndex((entry) => entry.id === commentId);
      if (index < 0) return current;
      const previous = current[index];
      const next = update(previous);
      if (next === previous) return current;
      const updated = current.slice();
      updated[index] = next;
      return updated;
    });
  };

  const abortVotes = () => {
    for (const controller of voteControllers.values()) controller.abort();
    voteControllers.clear();
    voteQueues.clear();
    voteSequences.clear();
    confirmedVotes.clear();
  };

  const loadPage = async (postId: number, after: number, requestEpoch: number) => {
    readController?.abort();
    const controller = new AbortController();
    readController = controller;
    setLoading(true);
    setLoadError(null);
    try {
      const page = await fetchComments(postId, after, 100, controller.signal);
      if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
      setComments((current) => mergeCommentsByID(current, page.comments));
      setNextAfter(page.nextAfter ?? 0);
      setCanModerate(page.canModerate);
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
    moderationController?.abort();
    moderationController = undefined;
    abortVotes();
    rowCache.clear();
    setComments([]);
    setNextAfter(0);
    setLoading(false);
    setLoadError(null);
    setDraft("");
    setReplyTo(null);
    setSubmitting(false);
    setSubmitError(null);
    setVoteError(null);
    setAuthBlocked(false);
    setCanModerate(false);
    setModeratingTarget(null);
    setModerationError(null);
    void loadPage(postId, 0, requestEpoch);

    onCleanup(() => {
      readController?.abort();
      createController?.abort();
      moderationController?.abort();
      moderationController = undefined;
      abortVotes();
      rowCache.clear();
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

  const voteOnComment = (commentId: number, direction: -1 | 1) => {
    if (!canCreate()) return;
    const current = comments().find((entry) => entry.id === commentId);
    if (!current || current.deleted) return;

    const postId = props.postId;
    const requestEpoch = epoch;
    const desiredVote = nextCommentVoteForDirection(current.userVote, direction) as PostVote;
    if (!confirmedVotes.has(commentId)) {
      confirmedVotes.set(commentId, { score: current.score, vote: current.userVote });
    }
    const sequence = (voteSequences.get(commentId) ?? 0) + 1;
    voteSequences.set(commentId, sequence);
    setVoteError((previous) => previous?.commentId === commentId ? null : previous);
    updateCommentByID(commentId, (entry) => withOptimisticCommentVote(entry, desiredVote));

    const previous = voteQueues.get(commentId) ?? Promise.resolve();
    let queued!: Promise<void>;
    queued = previous
      .catch(() => undefined)
      .then(async () => {
        if (requestEpoch !== epoch || props.postId !== postId) return;
        if (comments().find((entry) => entry.id === commentId)?.deleted !== false) return;
        const controller = new AbortController();
        voteControllers.set(commentId, controller);
        try {
          const result = await setCommentVote(postId, commentId, desiredVote, controller.signal);
          if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
          if (result.commentId !== commentId) throw new Error("comment vote response id mismatch");
          const confirmed = { score: result.score, vote: result.vote };
          confirmedVotes.set(commentId, confirmed);
          if (voteSequences.get(commentId) === sequence) {
            updateCommentByID(commentId, (entry) => ({
              ...entry,
              score: confirmed.score,
              userVote: confirmed.vote,
            }));
          }
        } catch (error) {
          if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
          if (voteSequences.get(commentId) !== sequence) return;
          const confirmed = confirmedVotes.get(commentId);
          if (confirmed) {
            updateCommentByID(commentId, (entry) => ({
              ...entry,
              score: confirmed.score,
              userVote: confirmed.vote,
            }));
          }
          if (error instanceof APIError && error.status === 401) {
            setAuthBlocked(true);
            setVoteError({ commentId, message: "Authentication required" });
          } else if (error instanceof APIError) {
            setVoteError({ commentId, message: error.message });
          } else {
            setVoteError({ commentId, message: "Vote update failed" });
          }
        } finally {
          if (voteControllers.get(commentId) === controller) voteControllers.delete(commentId);
        }
      })
      .finally(() => {
        if (requestEpoch !== epoch || props.postId !== postId) return;
        if (voteQueues.get(commentId) !== queued) return;
        voteQueues.delete(commentId);
        voteSequences.delete(commentId);
        confirmedVotes.delete(commentId);
      });
    voteQueues.set(commentId, queued);
  };

  const moderationFailure = (error: unknown, missingMessage: string) => {
    if (error instanceof APIError && (error.status === 401 || error.status === 403)) {
      setCanModerate(false);
      setModerationError(error.status === 401 ? "Authentication required" : "Moderation is not authorized");
      return;
    }
    if (error instanceof APIError && error.status === 404) {
      setModerationError(missingMessage);
      return;
    }
    setModerationError("Moderation update failed");
  };

  const moderatePost = async () => {
    if (!canModerate() || moderatingTarget() !== null) return;
    const postId = props.postId;
    const requestEpoch = epoch;
    moderationController?.abort();
    const controller = new AbortController();
    moderationController = controller;
    setModeratingTarget("post");
    setModerationError(null);

    try {
      const result = await hidePost(postId, controller.signal);
      if (!canApplyModerationResult(
        props.postId,
        postId,
        result.postId,
        requestEpoch,
        epoch,
        controller.signal.aborted,
      )) return;
      if (!result.deleted) throw new Error("post moderation response was not deleted");
      props.onPostModerated(postId);
    } catch (error) {
      if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
      moderationFailure(error, "Post is no longer available");
    } finally {
      if (moderationController === controller) moderationController = undefined;
      if (!controller.signal.aborted && requestEpoch === epoch && props.postId === postId) {
        setModeratingTarget(null);
      }
    }
  };

  const moderateComment = async (commentId: number) => {
    if (!canModerate() || moderatingTarget() !== null) return;
    const current = comments().find((entry) => entry.id === commentId);
    if (!current || current.deleted) return;

    const postId = props.postId;
    const requestEpoch = epoch;
    moderationController?.abort();
    const controller = new AbortController();
    moderationController = controller;
    setModeratingTarget(commentId);
    setModerationError(null);

    try {
      const result = await hideComment(postId, commentId, controller.signal);
      if (!canApplyModerationResult(
        props.postId,
        postId,
        result.postId,
        requestEpoch,
        epoch,
        controller.signal.aborted,
      )) return;
      if (result.commentId !== commentId || !result.deleted) {
        throw new Error("comment moderation response mismatch");
      }

      voteControllers.get(commentId)?.abort();
      voteControllers.delete(commentId);
      voteSequences.delete(commentId);
      confirmedVotes.delete(commentId);
      updateCommentByID(commentId, withModeratedComment);
      if (replyTo() === commentId) setReplyTo(null);
    } catch (error) {
      if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
      moderationFailure(error, "Comment is no longer available");
    } finally {
      if (moderationController === controller) moderationController = undefined;
      if (!controller.signal.aborted && requestEpoch === epoch && props.postId === postId) {
        setModeratingTarget(null);
      }
    }
  };

  return (
    <section class="comments" aria-label={`Comments for post ${props.postId}`}>
      <div class="comments-header">
        <strong>Comments</strong>
        <div class="comments-header-actions">
          <span>{comments().length.toLocaleString()} loaded</span>
          <Show when={canModerate()}>
            <button
              type="button"
              class="comment-moderate"
              disabled={moderatingTarget() !== null}
              onClick={() => void moderatePost()}
            >
              {moderatingTarget() === "post" ? "Hiding…" : "Hide post"}
            </button>
          </Show>
        </div>
      </div>

      <Show when={moderationError()}>
        {(message) => <p class="comment-error" role="alert">{message()}</p>}
      </Show>

      <Show when={canCreate()} fallback={<p class="comments-auth-note">Sign in to comment, reply, or vote.</p>}>
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
              <Show when={!row.comment.deleted}>
                <div class="comment-actions">
                  <Show when={canCreate()}>
                    <div class="comment-vote" role="group" aria-label={`Vote on comment ${row.comment.id}`}>
                      <button
                        type="button"
                        classList={{ "comment-vote--active": row.comment.userVote === 1 }}
                        aria-label={`Upvote comment ${row.comment.id}`}
                        aria-pressed={row.comment.userVote === 1}
                        onClick={() => voteOnComment(row.comment.id, 1)}
                      >▲</button>
                      <button
                        type="button"
                        classList={{ "comment-vote--active": row.comment.userVote === -1 }}
                        aria-label={`Downvote comment ${row.comment.id}`}
                        aria-pressed={row.comment.userVote === -1}
                        onClick={() => voteOnComment(row.comment.id, -1)}
                      >▼</button>
                    </div>
                  </Show>
                  <Show when={canCreate() && !row.orphaned}>
                    <button type="button" class="comment-reply" onClick={() => beginReply(row.comment.id)}>Reply</button>
                  </Show>
                  <Show when={canModerate()}>
                    <button
                      type="button"
                      class="comment-moderate"
                      disabled={moderatingTarget() !== null}
                      onClick={() => void moderateComment(row.comment.id)}
                    >
                      {moderatingTarget() === row.comment.id ? "Hiding…" : "Hide"}
                    </button>
                  </Show>
                </div>
              </Show>
              <Show when={voteError()?.commentId === row.comment.id}>
                <p class="comment-error" role="alert">{voteError()?.message}</p>
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
          onClick={() => void loadPage(props.postId, nextAfter(), epoch)}
        >
          {loading() ? "Loading…" : "Load more comments"}
        </button>
      </Show>
    </section>
  );
};

export default Comments;

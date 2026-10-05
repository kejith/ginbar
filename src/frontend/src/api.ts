export type MediaKind = 0 | 1;
export type ContentFilter = 0 | 1 | 2 | 3;
export type PostVote = -1 | 0 | 1;

export interface MediaSummary {
  kind: MediaKind;
  storageKey: string;
  mimeType: string;
  width: number;
  height: number;
  durationMs?: number;
}

export interface PostSummary {
  id: number;
  authorId: number;
  filter: ContentFilter;
  score: number;
  userVote: PostVote;
  createdAt: string;
  media: MediaSummary;
}

export interface FeedPage {
  posts: PostSummary[];
  nextBefore?: number;
}

export interface AroundResult {
  posts: PostSummary[];
  selectedId: number;
}

export interface CurrentUser {
  id: number;
  username: string;
}

export interface PostVoteResult {
  postId: number;
  score: number;
  vote: PostVote;
}

export interface TagItem {
  id: number;
  name: string;
}

export interface TagSnapshot {
  postId: number;
  tags: TagItem[];
  canRemove: boolean;
}

export interface Comment {
  id: number;
  postId: number;
  authorId: number;
  parentCommentId: number | null;
  body?: string;
  score: number;
  userVote: PostVote;
  createdAt: string;
  deleted: boolean;
}

export interface CommentPage {
  comments: Comment[];
  nextAfter?: number;
}

export interface CommentVoteResult {
  commentId: number;
  score: number;
  vote: PostVote;
}

interface AuthResponse {
  user: CurrentUser;
}

interface ErrorEnvelope {
  error?: {
    code?: string;
    message?: string;
  };
}

export class APIError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, message: string, code = "") {
    super(message);
    this.name = "APIError";
    this.status = status;
    this.code = code;
  }
}

export async function fetchCurrentUser(signal?: AbortSignal): Promise<CurrentUser | null> {
  const response = await fetch("/api/v2/auth/me", {
    signal,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (response.status === 401) return null;
  const body = await parseJSON<AuthResponse>(response, "current user");
  return body.user;
}

export async function fetchFeed(
  before = 0,
  limit = 120,
  searchQuery = "",
  signal?: AbortSignal,
): Promise<FeedPage> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (before > 0) query.set("before", String(before));
  if (searchQuery !== "") query.set("q", searchQuery);
  return parseJSON<FeedPage>(
    await fetch(`/api/v2/feed?${query}`, {
      signal,
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    }),
    "feed",
  );
}

export async function fetchAround(
  postId: number,
  radius: number,
  searchQuery = "",
  signal?: AbortSignal,
): Promise<AroundResult | null> {
  const query = new URLSearchParams({ radius: String(radius) });
  if (searchQuery !== "") query.set("q", searchQuery);
  const response = await fetch(`/api/v2/posts/${postId}/around?${query}`, {
    signal,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (response.status === 404) return null;
  return parseJSON<AroundResult>(response, "around post");
}

export async function setPostVote(postId: number, vote: PostVote): Promise<PostVoteResult> {
  return parseJSON<PostVoteResult>(
    await fetch(`/api/v2/posts/${postId}/vote`, {
      method: "PUT",
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ vote }),
    }),
    "post vote",
  );
}

export async function fetchPostTags(postId: number, signal?: AbortSignal): Promise<TagSnapshot> {
  return parseJSON<TagSnapshot>(
    await fetch(`/api/v2/posts/${postId}/tags`, {
      signal,
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    }),
    "post tags",
  );
}

export async function addPostTag(postId: number, name: string, signal?: AbortSignal): Promise<TagSnapshot> {
  return parseJSON<TagSnapshot>(
    await fetch(`/api/v2/posts/${postId}/tags`, {
      method: "POST",
      signal,
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ name }),
    }),
    "add post tag",
  );
}

export async function removePostTag(postId: number, tagId: number, signal?: AbortSignal): Promise<TagSnapshot> {
  return parseJSON<TagSnapshot>(
    await fetch(`/api/v2/posts/${postId}/tags/${tagId}`, {
      method: "DELETE",
      signal,
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    }),
    "remove post tag",
  );
}

export async function fetchComments(
  postId: number,
  after = 0,
  limit = 100,
  signal?: AbortSignal,
): Promise<CommentPage> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (after > 0) query.set("after", String(after));
  return parseJSON<CommentPage>(
    await fetch(`/api/v2/posts/${postId}/comments?${query}`, {
      signal,
      credentials: "same-origin",
      headers: { Accept: "application/json" },
    }),
    "comments",
  );
}

export async function createComment(
  postId: number,
  body: string,
  parentCommentId?: number,
  signal?: AbortSignal,
): Promise<Comment> {
  return parseJSON<Comment>(
    await fetch(`/api/v2/posts/${postId}/comments`, {
      method: "POST",
      signal,
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ body, ...(parentCommentId === undefined ? {} : { parentCommentId }) }),
    }),
    "comment",
  );
}

export async function setCommentVote(
  postId: number,
  commentId: number,
  vote: PostVote,
  signal?: AbortSignal,
): Promise<CommentVoteResult> {
  return parseJSON<CommentVoteResult>(
    await fetch(`/api/v2/posts/${postId}/comments/${commentId}/vote`, {
      method: "PUT",
      signal,
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ vote }),
    }),
    "comment vote",
  );
}

async function parseJSON<T>(response: Response, context: string): Promise<T> {
  if (!response.ok) {
    let code = "";
    let message = `${context} request failed: ${response.status}`;
    try {
      const body = await response.json() as ErrorEnvelope;
      if (typeof body.error?.code === "string") code = body.error.code;
      if (typeof body.error?.message === "string" && body.error.message !== "") message = body.error.message;
    } catch {
      // Preserve the bounded fallback message when the response is not JSON.
    }
    throw new APIError(response.status, message, code);
  }
  return await response.json() as T;
}

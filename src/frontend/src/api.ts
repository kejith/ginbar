export type MediaKind = 0 | 1;
export type ContentFilter = 0 | 1 | 2 | 3;

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

interface AuthResponse {
  user: CurrentUser;
}

export class APIError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "APIError";
    this.status = status;
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
  signal?: AbortSignal,
): Promise<FeedPage> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (before > 0) query.set("before", String(before));
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
  signal?: AbortSignal,
): Promise<AroundResult | null> {
  const response = await fetch(`/api/v2/posts/${postId}/around?radius=${radius}`, {
    signal,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (response.status === 404) return null;
  return parseJSON<AroundResult>(response, "around post");
}

async function parseJSON<T>(response: Response, context: string): Promise<T> {
  if (!response.ok) throw new APIError(response.status, `${context} request failed: ${response.status}`);
  return await response.json() as T;
}

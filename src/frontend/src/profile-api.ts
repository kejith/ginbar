import { APIError, type PostSummary } from "./api";

export interface PublicProfileUser {
  id: number;
  username: string;
  createdAt: string;
}

export interface ProfilePage {
  user: PublicProfileUser;
  posts: PostSummary[];
  nextBefore?: number;
}

interface ErrorEnvelope {
  error?: {
    code?: string;
    message?: string;
  };
}

export async function fetchProfile(
  userId: number,
  before = 0,
  limit = 33,
  signal?: AbortSignal,
): Promise<ProfilePage> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (before > 0) query.set("before", String(before));
  const response = await fetch(`/api/v2/users/${userId}?${query}`, {
    signal,
    credentials: "same-origin",
    headers: { Accept: "application/json" },
  });
  if (!response.ok) {
    let code = "";
    let message = `profile request failed: ${response.status}`;
    try {
      const body = await response.json() as ErrorEnvelope;
      if (typeof body.error?.code === "string") code = body.error.code;
      if (typeof body.error?.message === "string" && body.error.message !== "") message = body.error.message;
    } catch {
      // Preserve the bounded fallback message when the response is not JSON.
    }
    throw new APIError(response.status, message, code);
  }
  return await response.json() as ProfilePage;
}

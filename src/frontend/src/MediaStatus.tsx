import { Show, createEffect, createMemo, createSignal, onCleanup } from "solid-js";
import type { Component } from "solid-js";

import { mediaStatusPollDelay, mediaStatusText } from "./media-status.js";

type MediaStatusPhase = "waiting" | "processing" | "retrying" | "failed" | "ready";
type MediaStatusOperation = "initial" | "regeneration";

interface MediaStatusResponse {
  postId: number;
  phase: MediaStatusPhase;
  operation?: MediaStatusOperation;
  usableMedia: boolean;
  retryAt?: string;
  failure?: {
    code: string;
    message: string;
  };
}

interface MediaStatusProps {
  postId: number;
}

const MediaStatus: Component<MediaStatusProps> = (props) => {
  const [status, setStatus] = createSignal<MediaStatusResponse | null>(null);
  const [unavailable, setUnavailable] = createSignal(false);

  createEffect(() => {
    const postId = props.postId;
    setStatus(null);
    setUnavailable(false);

    let disposed = false;
    let timer: number | undefined;
    let controller: AbortController | undefined;

    const schedule = (value: MediaStatusResponse | null) => {
      const delay = mediaStatusPollDelay(value);
      if (delay !== null && !disposed) {
        timer = window.setTimeout(() => void load(), delay);
      }
    };

    const load = async () => {
      controller = new AbortController();
      try {
        const response = await fetch(`/api/v2/posts/${postId}/media-status`, {
          signal: controller.signal,
          headers: { Accept: "application/json" },
        });
        if (disposed) return;
        if (response.status === 404) {
          setStatus(null);
          setUnavailable(false);
          return;
        }
        if (!response.ok) throw new Error(`media status request failed: ${response.status}`);

        const value = await response.json() as MediaStatusResponse;
        if (value.postId !== postId) throw new Error("media status response post id mismatch");
        setStatus(value);
        setUnavailable(false);
        schedule(value);
      } catch (error) {
        if (disposed || controller?.signal.aborted) return;
        setUnavailable(true);
        schedule(status());
      }
    };

    void load();
    onCleanup(() => {
      disposed = true;
      if (timer !== undefined) window.clearTimeout(timer);
      controller?.abort();
    });
  });

  const text = createMemo(() => {
    const value = status();
    return value ? mediaStatusText(value) : "";
  });
  const visibleStatus = createMemo(() => {
    const value = status();
    return value?.phase === "ready" ? null : value;
  });

  return (
    <Show
      when={visibleStatus()}
      fallback={
        <Show when={unavailable()}>
          <p class="media-status media-status--unavailable" role="status">Processing status unavailable</p>
        </Show>
      }
    >
      {(value) => (
        <p
          class="media-status"
          classList={{ "media-status--failed": value().phase === "failed" }}
          data-phase={value().phase}
          data-operation={value().operation}
          role="status"
        >
          {text()}
        </p>
      )}
    </Show>
  );
};

export default MediaStatus;

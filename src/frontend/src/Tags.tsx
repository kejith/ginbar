import { For, Show, createEffect, createSignal, on, onCleanup } from "solid-js";
import type { Accessor, Component } from "solid-js";

import {
  APIError,
  addPostTag,
  fetchPostTags,
  removePostTag,
  type TagSnapshot,
} from "./api";
import { canApplyTagSnapshot } from "./tag-state.js";
import "./tags.css";

interface TagsProps {
  postId: number;
  canAdd: Accessor<boolean>;
}

const Tags: Component<TagsProps> = (props) => {
  let epoch = 0;
  let loadController: AbortController | undefined;
  let mutationController: AbortController | undefined;
  const [snapshot, setSnapshot] = createSignal<TagSnapshot | null>(null);
  const [loading, setLoading] = createSignal(true);
  const [mutating, setMutating] = createSignal(false);
  const [draft, setDraft] = createSignal("");
  const [error, setError] = createSignal<string | null>(null);

  createEffect(on(
    () => props.postId,
    (postId) => {
      const requestEpoch = ++epoch;
      loadController?.abort();
      mutationController?.abort();
      mutationController = undefined;
      setSnapshot(null);
      setLoading(true);
      setMutating(false);
      setDraft("");
      setError(null);

      const controller = new AbortController();
      loadController = controller;
      void fetchPostTags(postId, controller.signal)
        .then((result) => {
          if (!canApplyTagSnapshot(props.postId, postId, result.postId, requestEpoch, epoch, controller.signal.aborted)) return;
          setSnapshot(result);
        })
        .catch((requestError) => {
          if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
          if (requestError instanceof APIError && requestError.status === 404) setError("Post is no longer available");
          else setError("Tags could not be loaded");
        })
        .finally(() => {
          if (loadController !== controller || requestEpoch !== epoch) return;
          loadController = undefined;
          setLoading(false);
        });
    },
    { defer: false },
  ));

  onCleanup(() => {
    epoch += 1;
    loadController?.abort();
    mutationController?.abort();
  });

  const applyMutation = async (operation: "add" | "remove", tagId = 0) => {
    if (mutating()) return;
    const postId = props.postId;
    const requestEpoch = epoch;
    const name = draft();
    mutationController?.abort();
    const controller = new AbortController();
    mutationController = controller;
    setMutating(true);
    setError(null);

    try {
      const result = operation === "add"
        ? await addPostTag(postId, name, controller.signal)
        : await removePostTag(postId, tagId, controller.signal);
      if (!canApplyTagSnapshot(props.postId, postId, result.postId, requestEpoch, epoch, controller.signal.aborted)) return;
      setSnapshot(result);
      if (operation === "add") setDraft("");
    } catch (requestError) {
      if (controller.signal.aborted || requestEpoch !== epoch || props.postId !== postId) return;
      if (requestError instanceof APIError) {
        if (requestError.status === 401) setError("Authentication required");
        else if (requestError.status === 403) setError("Tag removal is not authorized");
        else if (requestError.code === "invalid_tag") setError("Tag name is invalid");
        else if (requestError.status === 404) setError("Post is no longer available");
        else setError("Tag update failed");
      } else {
        setError("Tag update failed");
      }
    } finally {
      if (mutationController !== controller || requestEpoch !== epoch) return;
      mutationController = undefined;
      setMutating(false);
    }
  };

  return (
    <section class="post-tags" data-post-tags={props.postId} aria-label={`Tags for post ${props.postId}`}>
      <div class="post-tags__header">
        <strong>Tags</strong>
        <Show when={loading()}><span>Loading…</span></Show>
      </div>

      <Show when={snapshot()}>
        {(current) => (
          <div class="post-tags__list">
            <Show when={current().tags.length > 0} fallback={<span class="post-tags__empty">No tags</span>}>
              <For each={current().tags}>
                {(item) => (
                  <span class="post-tag" data-tag-id={item.id}>
                    <span>{item.name}</span>
                    <Show when={current().canRemove}>
                      <button
                        type="button"
                        aria-label={`Remove tag ${item.name}`}
                        disabled={mutating()}
                        onClick={() => void applyMutation("remove", item.id)}
                      >
                        ×
                      </button>
                    </Show>
                  </span>
                )}
              </For>
            </Show>
          </div>
        )}
      </Show>

      <Show when={props.canAdd()}>
        <form
          class="post-tags__form"
          onSubmit={(event) => {
            event.preventDefault();
            if (draft().trim() !== "") void applyMutation("add");
          }}
        >
          <input
            type="text"
            value={draft()}
            aria-label="Add tag"
            placeholder="Add tag"
            disabled={mutating()}
            onInput={(event) => setDraft(event.currentTarget.value)}
          />
          <button type="submit" disabled={mutating() || draft().trim() === ""}>Add</button>
        </form>
      </Show>

      <Show when={error()}>
        {(message) => <p class="post-tags__error" role="alert">{message()}</p>}
      </Show>
    </section>
  );
};

export default Tags;

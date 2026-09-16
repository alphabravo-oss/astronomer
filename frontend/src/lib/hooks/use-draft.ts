import {
  useCallback,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";

/** Editable snapshot whose lifetime belongs to its server/source revision.
 * Reset during render when the source changes so children never paint a stale
 * draft, and never mutate the source object when applying local edits.
 */
export function useDraft<T>(
  source: T,
  revision: unknown = source,
): [T, Dispatch<SetStateAction<T>>] {
  const [state, setState] = useState({ revision, value: source });
  const value = Object.is(state.revision, revision) ? state.value : source;
  if (!Object.is(state.revision, revision))
    setState({ revision, value: source });
  const setValue: Dispatch<SetStateAction<T>> = useCallback(
    (action) => {
      setState((current) => {
        const previous = Object.is(current.revision, revision)
          ? current.value
          : source;
        return {
          revision,
          value:
            typeof action === "function"
              ? (action as (previous: T) => T)(previous)
              : action,
        };
      });
    },
    [source, revision],
  );
  return [value, setValue];
}

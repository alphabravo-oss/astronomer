import { useEffect, useRef, useState } from "react";
import { useMutation, type UseMutationOptions } from "@tanstack/react-query";

import {
  OperationPartialError,
  createIdempotencyKey,
  operationPhase,
  pollOperation,
  type OperationPhase,
  type OperationSnapshot,
} from "@/lib/api/operation-polling";

export interface OperationMutationState {
  phase: OperationPhase;
  operation?: OperationSnapshot;
}

export function useOperationMutation<
  TVariables,
  TOperation extends OperationSnapshot,
>(options: {
  keyPrefix: string;
  submit: (
    variables: TVariables,
    context: { idempotencyKey: string; signal: AbortSignal },
  ) => Promise<TOperation>;
  read: (
    id: string,
    signal?: AbortSignal,
    operation?: TOperation,
  ) => Promise<TOperation>;
  mutation?: Omit<
    UseMutationOptions<TOperation, Error, TVariables>,
    "mutationFn"
  >;
}) {
  const controllers = useRef(new Set<AbortController>());
  const mounted = useRef(true);
  const active = useRef(false);
  const [operationState, setOperationState] = useState<OperationMutationState>({
    phase: "idle",
  });

  useEffect(() => {
    mounted.current = true;
    const activeControllers = controllers.current;
    return () => {
      mounted.current = false;
      activeControllers.forEach((controller) => controller.abort());
      activeControllers.clear();
    };
  }, []);
  const updateState = (state: OperationMutationState) => {
    if (mounted.current) setOperationState(state);
  };

  const mutation = useMutation<TOperation, Error, TVariables>({
    ...options.mutation,
    mutationFn: async (variables) => {
      if (active.current) {
        throw new Error("An operation is already in progress");
      }
      active.current = true;
      const controller = new AbortController();
      controllers.current.add(controller);
      updateState({ phase: "submitting" });
      try {
        const receipt = await options.submit(variables, {
          idempotencyKey: createIdempotencyKey(options.keyPrefix),
          signal: controller.signal,
        });
        return await pollOperation(receipt, options.read, {
          signal: controller.signal,
          onUpdate: (operation) =>
            updateState({
              phase: operationPhase(operation.status),
              operation,
            }),
        });
      } catch (error) {
        if (
          (error as Error).name !== "AbortError" &&
          !(error instanceof OperationPartialError)
        ) {
          if (mounted.current)
            setOperationState((current) => ({ ...current, phase: "failed" }));
        }
        throw error;
      } finally {
        active.current = false;
        controllers.current.delete(controller);
      }
    },
  });

  return { ...mutation, operationState };
}

import { useMemo, useState, type KeyboardEvent } from "react";
import {
  commandInsertion,
  commandSuggestions,
  contextualCharlieCommands,
  parseCharlieCommand,
} from "./commands";
import type { CharlieConversation } from "./use-charlie-conversation";
import type { CharlieModePresentation } from "./charlie-mode";

/** Composer state and slash-command dispatch, including keyboard acceptance and client-only commands. */
export function useCharlieComposer(
  conversation: CharlieConversation,
  pathname: string,
  mode: CharlieModePresentation,
) {
  const {
    commands,
    viewingThreadId,
    historyReady,
    send,
    awaitingReply,
    sessionId,
    startNewChat,
    stickToBottomRef,
    setConfirmAbort,
  } = conversation;
  const [text, setTextValue] = useState("");
  const [scopePickerOpen, setScopePickerOpen] = useState(false);
  const [commandHelpOpen, setCommandHelpOpen] = useState(false);
  const [commandNotice, setCommandNotice] = useState<string>();
  const [selectedCommandIndex, setSelectedCommandIndex] = useState(0);
  const resetAfterNewChat = () => {
    setCommandHelpOpen(false);
    setCommandNotice(undefined);
  };
  const setText = (value: string) => {
    setTextValue(value);
    setSelectedCommandIndex(0);
  };
  const catalogCommands = useMemo(
    () => commands.data?.commands ?? [],
    [commands.data?.commands],
  );
  const slashSuggestions = useMemo(
    () => commandSuggestions(text, catalogCommands),
    [catalogCommands, text],
  );
  const suggestedCommands = useMemo(
    () => contextualCharlieCommands(pathname, catalogCommands),
    [catalogCommands, pathname],
  );

  const submitComposer = () => {
    const value = text.trim();
    if (
      !value ||
      viewingThreadId ||
      !historyReady ||
      send.isPending ||
      awaitingReply
    )
      return;
    if (value.startsWith("/")) {
      const parsed = parseCharlieCommand(value, catalogCommands);
      if (!parsed) {
        setCommandNotice(
          commands.isError
            ? "The command catalog is unavailable. Natural-language chat is still available."
            : "Unknown or incomplete command. Choose a suggestion or use /help.",
        );
        return;
      }
      if (parsed.descriptor.execution === "client") {
        setText("");
        setCommandNotice(undefined);
        switch (parsed.descriptor.id) {
          case "help":
            setCommandHelpOpen(true);
            break;
          case "scope":
            setScopePickerOpen(true);
            break;
          case "mode":
            setCommandNotice(`Charlie is in ${mode.label}. ${mode.ceiling}`);
            break;
          case "new":
            send.reset();
            startNewChat.mutate();
            break;
          case "stop":
            if (sessionId) setConfirmAbort(true);
            else
              setCommandNotice("There is no active Charlie session to stop.");
            break;
        }
        return;
      }
      setText("");
      setCommandNotice(undefined);
      stickToBottomRef.current = true;
      send.mutate({ message: value, command: parsed.request });
      return;
    }
    setText("");
    setCommandNotice(undefined);
    stickToBottomRef.current = true;
    send.mutate({ message: value });
  };

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.nativeEvent.isComposing) return;
    if (
      slashSuggestions.length > 0 &&
      (e.key === "ArrowDown" || e.key === "ArrowUp")
    ) {
      e.preventDefault();
      setSelectedCommandIndex((current) =>
        e.key === "ArrowDown"
          ? (current + 1) % slashSuggestions.length
          : (current - 1 + slashSuggestions.length) % slashSuggestions.length,
      );
      return;
    }
    if (slashSuggestions.length > 0 && e.key === "Tab") {
      e.preventDefault();
      setText(
        commandInsertion(
          slashSuggestions[selectedCommandIndex] ?? slashSuggestions[0],
        ),
      );
      return;
    }
    // Enter sends a complete command/message; for a partial command
    // it accepts the highlighted suggestion. Shift+Enter is newline.
    if (e.key !== "Enter" || e.shiftKey) return;
    e.preventDefault();
    if (
      slashSuggestions.length > 0 &&
      !parseCharlieCommand(text, catalogCommands)
    ) {
      setText(
        commandInsertion(
          slashSuggestions[selectedCommandIndex] ?? slashSuggestions[0],
        ),
      );
      return;
    }
    submitComposer();
  }
  return {
    resetAfterNewChat,
    text,
    setText,
    scopePickerOpen,
    setScopePickerOpen,
    commandHelpOpen,
    setCommandHelpOpen,
    commandNotice,
    setCommandNotice,
    selectedCommandIndex,
    catalogCommands,
    slashSuggestions,
    suggestedCommands,
    submitComposer,
    onKeyDown,
  };
}
export type CharlieComposerModel = ReturnType<typeof useCharlieComposer>;

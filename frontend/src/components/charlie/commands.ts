import type {
  CharlieCommandDescriptor,
  CharlieCommandRequest,
} from "@/lib/api/charlie";

export type ParsedCharlieCommand = {
  descriptor: CharlieCommandDescriptor;
  request: CharlieCommandRequest;
};

function commandName(input: string): string {
  return input.trimStart().slice(1).split(/\s/, 1)[0]?.toLowerCase() ?? "";
}

export function commandSuggestions(
  input: string,
  commands: CharlieCommandDescriptor[],
): CharlieCommandDescriptor[] {
  if (!input.trimStart().startsWith("/")) return [];
  const query = commandName(input);
  return commands
    .filter((command) =>
      !query ||
      command.name.startsWith(query) ||
      command.aliases?.some((alias) => alias.startsWith(query)) ||
      command.label.toLowerCase().includes(query) ||
      command.description.toLowerCase().includes(query),
    )
    .slice(0, 8);
}

export function commandInsertion(command: CharlieCommandDescriptor): string {
  return `/${command.name}${command.argument ? " " : ""}`;
}

export function parseCharlieCommand(
  input: string,
  commands: CharlieCommandDescriptor[],
): ParsedCharlieCommand | undefined {
  const visible = input.trim();
  if (!visible.startsWith("/")) return undefined;
  const name = commandName(visible);
  const descriptor = commands.find(
    (command) => command.name === name || command.aliases?.includes(name),
  );
  if (!descriptor) return undefined;
  const firstWhitespace = visible.search(/\s/);
  const argument = firstWhitespace < 0 ? "" : visible.slice(firstWhitespace).trim();
  const args: Record<string, string> = {};
  if (descriptor.argument) {
    if (descriptor.argument.required && !argument) return undefined;
    if (
      [...argument].length > 512 ||
      [...argument].some((character) => {
        const code = character.codePointAt(0) ?? 0;
        return code < 32 && code !== 9 && code !== 10 && code !== 13;
      })
    ) return undefined;
    if (argument) args[descriptor.argument.name] = argument;
  } else if (argument) {
    return undefined;
  }
  return {
    descriptor,
    request: { id: descriptor.id, version: descriptor.version, arguments: args },
  };
}

export function contextualCharlieCommands(
  pathname: string,
  commands: CharlieCommandDescriptor[],
): CharlieCommandDescriptor[] {
  const preferred = pathname.includes("queue")
    ? "queues"
    : pathname.includes("agent") || pathname.includes("cluster")
      ? "agents"
      : pathname.includes("alert")
        ? "alerts"
        : pathname.includes("backup")
          ? "backups"
          : "health";
  const ids = [preferred, "health", "issues", "help"];
  return ids.flatMap((id, index) => {
    if (ids.indexOf(id) !== index) return [];
    const command = commands.find((candidate) => candidate.id === id);
    return command ? [command] : [];
  });
}

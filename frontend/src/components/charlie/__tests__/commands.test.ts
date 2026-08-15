import { describe, expect, it } from "vitest";
import type { CharlieCommandDescriptor } from "@/lib/api/charlie";
import {
  commandInsertion,
  commandSuggestions,
  parseCharlieCommand,
} from "../commands";

const commands: CharlieCommandDescriptor[] = [
  { id: "health", version: "1", name: "health", aliases: ["system-health"], label: "System health", description: "Assess health", category: "Assess", execution: "agent", effect: "read", required_mode: "read_only", example: "/health" },
  { id: "investigate", version: "1", name: "investigate", label: "Investigate", description: "Investigate a subject", category: "Investigate", execution: "agent", effect: "read", required_mode: "read_only", example: "/investigate queues", argument: { name: "subject", placeholder: "subject", required: true } },
];

describe("Charlie command composer helpers", () => {
  it("matches canonical names and aliases without treating prose as commands", () => {
    expect(commandSuggestions("/hea", commands).map((item) => item.id)).toEqual(["health"]);
    expect(parseCharlieCommand("/system-health", commands)?.request).toEqual({ id: "health", version: "1", arguments: {} });
    expect(parseCharlieCommand("please check health", commands)).toBeUndefined();
  });

  it("requires and bounds command shape before building the server request", () => {
    expect(parseCharlieCommand("/investigate", commands)).toBeUndefined();
    expect(parseCharlieCommand("/investigate queue failures", commands)?.request.arguments).toEqual({ subject: "queue failures" });
    expect(parseCharlieCommand(`/investigate ${"x".repeat(513)}`, commands)).toBeUndefined();
    expect(commandInsertion(commands[1])).toBe("/investigate ");
  });
});

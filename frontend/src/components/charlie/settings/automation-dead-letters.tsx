import type { UseQueryResult } from "@tanstack/react-query";
import { RefreshCw } from "lucide-react";
import { StatusBadge } from "@/components/ui/status-badge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
import { formatRelativeTime } from "@/lib/utils";
import type { CharlieTriggerEvent } from "@/lib/api/charlie-admin";
import { Section, Unavailable, button } from "./shared";
import { ActionButton } from "@/components/ui/action-button";
import { Tooltip } from "@/components/ui/tooltip";

export function AutomationDeadLetters({
  deadLetters,
  onRetry,
}: {
  deadLetters: UseQueryResult<CharlieTriggerEvent[]>;
  onRetry: (event: CharlieTriggerEvent) => void;
}) {
  return (
    <Section
      title="Dead-letter events"
      description="Bounded lifecycle metadata for failed trigger work. Charlie task content, fingerprints, sessions, and origin details are never exposed here."
    >
      {deadLetters.isLoading ? (
        <p role="status" className="text-sm text-muted-foreground">
          Loading dead-letter events…
        </p>
      ) : deadLetters.isError ? (
        <Unavailable
          name="Dead-letter events"
          retry={() => void deadLetters.refetch()}
        />
      ) : deadLetters.data?.length ? (
        <div className="overflow-x-auto">
          <Table layout="scroll" className="w-full min-w-190 text-left text-sm">
            <caption className="sr-only">
              Charlie dead-letter trigger lifecycle metadata
            </caption>
            <TableHeader className="text-xs text-muted-foreground">
              <TableRow>
                <TableHead scope="col" className="px-2 py-2">
                  Event
                </TableHead>
                <TableHead scope="col" className="px-2 py-2">
                  Resource
                </TableHead>
                <TableHead scope="col" className="px-2 py-2">
                  State
                </TableHead>
                <TableHead scope="col" className="px-2 py-2">
                  Attempts
                </TableHead>
                <TableHead scope="col" className="px-2 py-2">
                  Last error
                </TableHead>
                <TableHead scope="col" className="px-2 py-2">
                  Dead-lettered
                </TableHead>
                <TableHead scope="col" className="px-2 py-2">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody className="divide-y divide-border">
              {deadLetters.data.map((event) => (
                <TableRow key={event.id}>
                  <TableCell className="px-2 py-2">
                    <span className="block font-medium">{event.eventType}</span>
                    <Tooltip content={event.id}>
                      <span className="block max-w-48 truncate font-mono text-xs text-muted-foreground">
                        {event.id}
                      </span>
                    </Tooltip>
                  </TableCell>
                  <TableCell className="px-2 py-2">
                    {event.resourceType} · {event.resourceId}
                  </TableCell>
                  <TableCell className="px-2 py-2">
                    <StatusBadge status={event.state} />
                  </TableCell>
                  <TableCell className="px-2 py-2">
                    {event.attemptCount}
                  </TableCell>
                  <TableCell className="px-2 py-2">
                    {event.lastErrorCode || "—"}
                  </TableCell>
                  <TableCell className="px-2 py-2">
                    {event.deadLetteredAt
                      ? formatRelativeTime(event.deadLetteredAt)
                      : "—"}
                  </TableCell>
                  <TableCell className="px-2 py-2 text-right">
                    <ActionButton
                      intent="bare"
                      size="none"
                      className={button}
                      onClick={() => onRetry(event)}
                    >
                      <RefreshCw className="h-4 w-4" />
                      Retry
                    </ActionButton>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          No dead-letter trigger events.
        </p>
      )}
    </Section>
  );
}

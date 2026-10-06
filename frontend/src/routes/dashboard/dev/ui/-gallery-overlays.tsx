import { useState } from "react";
import { Server, Trash2 } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { ActionMenu } from "@/components/ui/action-menu";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  EmptyState,
  ErrorState,
  LoadingState,
  PermissionState,
} from "@/components/ui/empty-state";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { QueryStates } from "@/components/ui/query-states";
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { Tooltip } from "@/components/ui/tooltip";
import { Row } from "./-gallery-shared";

export function OverlaysDemo() {
  const [confirm, setConfirm] = useState(false);
  const [typed, setTyped] = useState(false);
  return (
    <>
      <Row>
        <Tooltip content="Tooltip content">
          <ActionButton>Tooltip</ActionButton>
        </Tooltip>
        <Tooltip content="Why this is disabled" wrap>
          <ActionButton disabled>Disabled tooltip</ActionButton>
        </Tooltip>
        <Popover>
          <PopoverTrigger asChild>
            <ActionButton>Popover</ActionButton>
          </PopoverTrigger>
          <PopoverContent>
            <p className="text-sm">Popover content</p>
          </PopoverContent>
        </Popover>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <ActionButton>Menu</ActionButton>
          </DropdownMenuTrigger>
          <DropdownMenuContent>
            <DropdownMenuLabel>Cluster</DropdownMenuLabel>
            <DropdownMenuItem>Cordon</DropdownMenuItem>
            <DropdownMenuItem>Drain</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem>Remove</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
        <ActionMenu
          ariaLabel="Row actions"
          items={[
            { label: "Edit", onClick: () => {} },
            { label: "Disabled", onClick: () => {}, disabled: true },
            {
              label: "Delete",
              onClick: () => {},
              variant: "destructive",
              separator: true,
              icon: <Trash2 className="h-4 w-4" />,
            },
          ]}
        />
      </Row>
      <Row>
        <Dialog>
          <DialogTrigger asChild>
            <ActionButton>Dialog</ActionButton>
          </DialogTrigger>
          <DialogContent>
            <DialogTitle className="text-lg font-semibold">Dialog</DialogTitle>
            <DialogDescription className="mt-1 text-sm text-muted-foreground">
              Radix dialog with focus trap and scroll lock.
            </DialogDescription>
            <div className="mt-4 flex justify-end">
              <DialogClose asChild>
                <ActionButton>Close</ActionButton>
              </DialogClose>
            </div>
          </DialogContent>
        </Dialog>
        <Sheet>
          <SheetTrigger asChild>
            <ActionButton>Sheet</ActionButton>
          </SheetTrigger>
          <SheetContent className="p-6">
            <SheetTitle className="text-lg font-semibold">Sheet</SheetTitle>
            <SheetDescription className="mt-1 text-sm text-muted-foreground">
              Edge-anchored panel.
            </SheetDescription>
            <div className="mt-4">
              <SheetClose asChild>
                <ActionButton>Close</ActionButton>
              </SheetClose>
            </div>
          </SheetContent>
        </Sheet>
        <ActionButton intent="destructive" onClick={() => setConfirm(true)}>
          Confirm dialog
        </ActionButton>
        <ActionButton intent="destructive" onClick={() => setTyped(true)}>
          Typed confirm
        </ActionButton>
      </Row>
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        onConfirm={() => setConfirm(false)}
        variant="destructive"
        title="Delete deployment"
        description="This removes the deployment from the cluster."
      />
      <ConfirmDialog
        open={typed}
        onClose={() => setTyped(false)}
        onConfirm={() => setTyped(false)}
        variant="destructive"
        title="Delete cluster"
        description="Type the cluster name to confirm."
        confirmValue="prod-eu-1"
        impact={{
          scope: "Cluster prod-eu-1",
          consequences: ["Agent is removed", "Fleet history is retained"],
          recovery: "Re-register the cluster to restore access.",
        }}
      />
    </>
  );
}

const noop = () => {};

export function StatesDemo() {
  return (
    <div className="space-y-4">
      <EmptyState
        icon={Server}
        title="No clusters yet"
        description="Register a cluster to get started."
        actionLabel="Register cluster"
        onAction={noop}
      />
      <EmptyState
        icon={Server}
        title="Nothing to do"
        description="Terminal empty state."
        terminal
      />
      <LoadingState title="Loading clusters" />
      <ErrorState description="The API did not respond." onRetry={noop} />
      <PermissionState permission="clusters:read" />
      <QueryStates
        query={{ isLoading: true, isError: false, refetch: noop }}
        loadingTitle="QueryStates loading"
      >
        <div />
      </QueryStates>
      <QueryStates
        query={{
          isLoading: false,
          isError: true,
          error: new Error("boom"),
          refetch: noop,
        }}
        errorTitle="QueryStates error"
      >
        <div />
      </QueryStates>
      <QueryStates
        query={{ data: [], isLoading: false, isError: false, refetch: noop }}
        isEmpty={(rows: unknown[]) => rows.length === 0}
        empty={<div className="text-sm">QueryStates empty slot</div>}
      >
        <div />
      </QueryStates>
    </div>
  );
}

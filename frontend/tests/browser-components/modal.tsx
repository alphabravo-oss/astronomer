import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SIEMForwarderModal } from "@/routes/dashboard/settings/siem/-forwarder-modal";
const client = new QueryClient({
  defaultOptions: { mutations: { retry: false } },
});
export function ModalFixture() {
  const [open, setOpen] = useState(true);
  return (
    <QueryClientProvider client={client}>
      {open ? (
        <SIEMForwarderModal forwarder={null} onClose={() => setOpen(false)} />
      ) : (
        <p>Saved and closed</p>
      )}
    </QueryClientProvider>
  );
}

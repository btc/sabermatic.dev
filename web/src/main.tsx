import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TransportProvider } from "@connectrpc/connect-query";
import { BrowserRouter } from "react-router-dom";
import { ErrorBoundary } from "react-error-boundary";
import { Toaster } from "sonner";
import { App } from "./app";
import { ErrorFallback } from "./components/error-fallback";
import { initTelemetry } from "./telemetry/provider";
import { transport } from "./api/transport";
import "./index.css";

initTelemetry();

// Prime CSRF token — Gorilla CSRF sets the cookie on every response and
// exposes the masked token via the X-CSRF-Token response header. This GET
// ensures the token is available before any POST.
fetch("/api/health", { credentials: "same-origin" }).then((res) => {
  const token = res.headers.get("X-CSRF-Token");
  if (token) window.__csrfToken = token;
});

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ErrorBoundary
      FallbackComponent={ErrorFallback}
      onReset={() => window.location.reload()}
    >
      <QueryClientProvider client={queryClient}>
        <TransportProvider transport={transport}>
          <BrowserRouter>
            <App />
          </BrowserRouter>
        </TransportProvider>
      </QueryClientProvider>
    </ErrorBoundary>
    <Toaster theme="dark" position="bottom-right" richColors />
  </StrictMode>,
);

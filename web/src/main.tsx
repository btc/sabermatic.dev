import "./index.css";

import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ErrorBoundary } from "react-error-boundary";
import { BrowserRouter } from "react-router-dom";
import { Toaster } from "sonner";

import { transport } from "./api/transport";
import { App } from "./app";
import { ErrorFallback } from "./components/error-fallback";
import { ThemeProvider } from "./contexts/theme-context";
import { useTheme } from "./hooks/use-theme";
import { initTelemetry } from "./telemetry/provider";

initTelemetry();

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
});

function ThemedToaster() {
  const { resolvedTheme } = useTheme();
  return <Toaster theme={resolvedTheme} position="bottom-right" richColors />;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
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
      <ThemedToaster />
    </ThemeProvider>
  </StrictMode>,
);

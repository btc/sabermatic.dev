import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";
import { App } from "./app";
import { initTelemetry } from "./telemetry/provider";
import "./index.css";

initTelemetry();

// Prime CSRF cookie — Gorilla CSRF sets the cookie on every response.
// In dev, the SPA is served by Vite (port 3000), so no Go server response
// has occurred yet when the user lands on /login. This GET ensures the
// drill_csrf cookie exists before any POST.
fetch("/api/health", { credentials: "same-origin" });

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
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);

import { createContext, useContext } from "react";

type DataSource = "api" | "sample";

interface SessionDetailContext {
  dataSource: DataSource;
  sessionId: string;
}

export const SessionDetailCtx = createContext<SessionDetailContext>({
  dataSource: "api",
  sessionId: "",
});

export function useSessionDetail() {
  return useContext(SessionDetailCtx);
}

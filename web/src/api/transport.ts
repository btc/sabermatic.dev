import { createConnectTransport } from "@connectrpc/connect-web";

export const transport = createConnectTransport({
  baseUrl: "/",
  // No CSRF token needed — Connect's Content-Type header provides implicit
  // CSRF protection. See design spec section 4.
  //
  // credentials: "same-origin" ensures the session cookie is sent with each
  // request. The fetch override is required because ConnectTransportOptions
  // does not expose credentials directly.
  fetch: (input, init) =>
    globalThis.fetch(input, { ...init, credentials: "same-origin" }),
});

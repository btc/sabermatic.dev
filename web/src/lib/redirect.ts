/**
 * Returns `raw` as a same-origin path (pathname + search + hash) if it is one,
 * otherwise "/". Used for the `?redirect=` hint on the auth pages, which is
 * attacker-controllable via a crafted link.
 *
 * Prefix checks alone miss browser URL normalization: "\" is treated as "/"
 * in http(s) URLs and ASCII tab/newline are stripped, so "/\evil.com" and
 * "/\t/evil.com" both resolve to "//evil.com". Resolving with the URL parser
 * and comparing origins covers those cases. The normalized path is checked
 * too: dot segments collapse during parsing, so "/.//evil.com" stays
 * same-origin as a URL but its pathname "//evil.com" is protocol-relative
 * when handed to navigate().
 */
export function safeRedirect(raw: string | null | undefined): string {
  if (!raw || !raw.startsWith("/")) return "/";
  let url: URL;
  try {
    url = new URL(raw, window.location.origin);
  } catch {
    return "/";
  }
  if (url.origin !== window.location.origin) return "/";
  const path = url.pathname + url.search + url.hash;
  return path.startsWith("//") ? "/" : path;
}

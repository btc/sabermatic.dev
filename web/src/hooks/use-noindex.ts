import { useEffect } from "react";

/**
 * useNoindex sets a `<meta name="robots" content="noindex">` tag on mount and
 * removes it on unmount. Use on auth-flow pages that should not appear in
 * search results.
 */
export function useNoindex() {
  useEffect(() => {
    const meta = document.createElement("meta");
    meta.name = "robots";
    meta.content = "noindex";
    document.head.appendChild(meta);
    return () => {
      document.head.removeChild(meta);
    };
  }, []);
}

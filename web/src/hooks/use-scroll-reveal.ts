import { useCallback, useEffect, useState } from "react";

interface ScrollRevealOptions {
  threshold?: number;
  rootMargin?: string;
  /** If true, only triggers once (default: true) */
  once?: boolean;
}

export function useScrollReveal<T extends HTMLElement>(
  options: ScrollRevealOptions = {},
) {
  const { threshold = 0.2, rootMargin = "0px", once = true } = options;
  const [node, setNode] = useState<T | null>(null);
  const [isVisible, setIsVisible] = useState(false);

  // Callback ref so the observer rebinds whenever the underlying DOM node
  // mounts or unmounts. A useRef-based version missed sections that initially
  // returned null (e.g. gated on async sample data) and then late-mounted —
  // the effect's deps were stable so it never re-ran on the new node.
  const ref = useCallback((n: T | null) => setNode(n), []);

  useEffect(() => {
    if (!node) return;

    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0];
        if (!entry) return;
        if (entry.isIntersecting) {
          setIsVisible(true);
          if (once) observer.unobserve(node);
        } else if (!once) {
          setIsVisible(false);
        }
      },
      { threshold, rootMargin },
    );

    observer.observe(node);
    return () => observer.disconnect();
  }, [node, threshold, rootMargin, once]);

  return { ref, isVisible };
}

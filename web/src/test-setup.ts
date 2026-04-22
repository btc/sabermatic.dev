import "@testing-library/jest-dom/vitest";

import { vi } from "vitest";

// jsdom doesn't implement IntersectionObserver; stub so scroll-reveal hooks don't throw.
const mockIntersectionObserver = vi.fn().mockImplementation(() => ({
  observe: vi.fn(),
  unobserve: vi.fn(),
  disconnect: vi.fn(),
}));
vi.stubGlobal("IntersectionObserver", mockIntersectionObserver);

// jsdom doesn't implement matchMedia; stub so prefers-reduced-motion checks don't throw.
vi.stubGlobal(
  "matchMedia",
  vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
);

export function Kbd({ children }: { children: React.ReactNode }) {
  return (
    <kbd className="px-1 py-0.5 rounded border border-border bg-muted text-[10px] font-mono">
      {children}
    </kbd>
  );
}

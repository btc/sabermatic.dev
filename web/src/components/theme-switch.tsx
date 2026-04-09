import { Sun, Moon, Monitor } from "lucide-react";
import { useTheme, type Theme } from "@/hooks/use-theme";
import { cn } from "@/lib/utils";

const OPTIONS: { value: Theme; icon: typeof Sun; label: string }[] = [
  { value: "light", icon: Sun, label: "Light" },
  { value: "dark", icon: Moon, label: "Dark" },
  { value: "system", icon: Monitor, label: "System" },
];

export function ThemeSwitch() {
  const { theme, setTheme } = useTheme();

  return (
    <div className="flex items-center rounded-lg border border-border bg-muted/40 p-0.5 gap-0.5 w-fit">
      {OPTIONS.map((opt) => (
        <button
          key={opt.value}
          type="button"
          title={opt.label}
          onClick={(e) => {
            e.stopPropagation(); // prevent Radix dropdown from closing
            setTheme(opt.value);
          }}
          className={cn(
            "rounded-md p-1.5 transition-colors cursor-pointer",
            theme === opt.value
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          <opt.icon className="h-3.5 w-3.5" />
        </button>
      ))}
    </div>
  );
}

import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

// EmptyState is the shared empty placeholder for list views: one visual
// language (centered icon + title + hint) instead of each page improvising
// its own muted paragraph.
export function EmptyState({ icon: Icon, title, desc, className }: {
  icon: LucideIcon;
  title: string;
  desc?: string;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center justify-center gap-2 px-6 py-12 text-center", className)}>
      <span className="flex h-10 w-10 items-center justify-center rounded-full bg-muted text-muted-foreground">
        <Icon className="h-5 w-5" aria-hidden />
      </span>
      <div className="text-sm font-medium">{title}</div>
      {desc && <div className="max-w-md text-xs text-muted-foreground">{desc}</div>}
    </div>
  );
}

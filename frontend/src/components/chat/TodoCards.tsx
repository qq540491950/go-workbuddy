import { Check, ListTodo, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/** TodoItem mirrors the plan_todos tool payload. */
export type TodoItem = { content: string; status: string };

/** TodoListCard renders the model's session plan (plan_todos result) as a
 * checklist so multi-step work is legible at a glance. */
export function TodoListCard({ resp }: { resp: { [k: string]: any } | null }) {
  const todos: TodoItem[] = Array.isArray(resp?.todos) ? resp.todos : [];
  if (todos.length === 0) return null;
  const done = todos.filter((t) => t.status === "completed").length;
  return (
    <div className="rounded-lg border px-4 py-3">
      <div className="mb-2 flex items-center gap-2">
        <ListTodo className="h-4 w-4 text-primary" />
        <span className="text-sm font-medium">任务计划</span>
        <Badge variant="outline" className="font-normal text-[11px]">
          {done}/{todos.length} 完成
        </Badge>
      </div>
      <ul className="space-y-1.5">
        {todos.map((t, i) => (
          <li key={i} className="flex items-start gap-2 text-sm">
            {t.status === "completed" ? (
              <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-600" />
            ) : t.status === "in_progress" ? (
              <Loader2 className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin text-primary" />
            ) : (
              <span className="mt-0.5 inline-block h-3.5 w-3.5 shrink-0 rounded-full border border-muted-foreground/50" />
            )}
            <span className={cn(
              "break-words",
              t.status === "completed" && "text-muted-foreground line-through",
              t.status === "in_progress" && "font-medium",
            )}>
              {t.content}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** PlanStrip is the compact pinned view of the model's session plan. */
export function PlanStrip({ todos }: { todos: TodoItem[] }) {
  const done = todos.filter((t) => t.status === "completed").length;
  const current = todos.find((t) => t.status === "in_progress");
  return (
    <div className="rounded-lg border bg-muted/30 px-3 py-2 text-xs">
      <div className="flex items-center gap-2">
        <ListTodo className="h-3.5 w-3.5 shrink-0 text-primary" />
        <span className="shrink-0 font-medium">任务计划</span>
        <span className="shrink-0 text-muted-foreground">{done}/{todos.length} 完成</span>
        {current && <span className="min-w-0 flex-1 truncate text-muted-foreground">· 当前：{current.content}</span>}
        <div className="ml-auto h-1 w-16 shrink-0 overflow-hidden rounded-full bg-muted">
          <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${(done / todos.length) * 100}%` }} />
        </div>
      </div>
    </div>
  );
}

import { memo, useState } from "react";
import {
  Check, Copy, Loader2, Pencil, RefreshCw, ShieldAlert, Volume2, X,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { MessageMarkdown, CopyButton } from "@/components/MessageMarkdown";
import { ToolCallCard } from "@/components/ToolCallCard";
import { TodoListCard } from "@/components/chat/TodoCards";
import { dayLabel, fmtTime, type UIMessage } from "@/lib/chatUtils";
import type { UsageInfo } from "@/lib/api";
import { cn } from "@/lib/utils";

/** ConfirmCard renders a HITL tool-confirmation request with approve/reject. */
export function ConfirmCard({ m, onConfirm }: {
  m: UIMessage;
  onConfirm: (msgId: string, callId: string | undefined, approved: boolean) => void;
  durationMs?: number;
}) {
  const status = m.confirmStatus ?? "pending";
  return (
    <div className={cn(
      "rounded-lg border px-4 py-3",
      status === "pending" && "border-amber-500/50 bg-amber-500/10",
      status === "approved" && "border-emerald-500/40 bg-emerald-500/5",
      status === "rejected" && "border-destructive/40 bg-destructive/5",
    )}>
      <div className="flex items-center gap-2">
        <ShieldAlert className={cn(
          "h-4 w-4 shrink-0",
          status === "pending" && "text-amber-600",
          status === "approved" && "text-emerald-600",
          status === "rejected" && "text-destructive",
        )} />
        <span className="text-sm font-medium">
          {status === "pending" ? "工具执行确认" : status === "approved" ? "已批准执行" : "已拒绝"}
        </span>
        <Badge variant="outline" className="font-mono text-[11px]">{m.toolName}</Badge>
      </div>
      {m.hint && <div className="mt-1.5 text-xs text-muted-foreground">{m.hint}</div>}
      {m.toolArgs && Object.keys(m.toolArgs).length > 0 && (
        <pre className="mt-2 max-h-32 overflow-auto rounded bg-muted/60 p-2 font-mono text-[11px] leading-relaxed text-muted-foreground">
          {JSON.stringify(m.toolArgs, null, 2)}
        </pre>
      )}
      {status === "pending" ? (
        <div className="mt-2.5 flex gap-2">
          <Button size="sm" className="h-7 px-3 text-xs" onClick={() => onConfirm(m.id, m.callId, true)}>
            <Check className="mr-1 h-3 w-3" /> 允许执行
          </Button>
          <Button size="sm" variant="outline" className="h-7 px-3 text-xs" onClick={() => onConfirm(m.id, m.callId, false)}>
            <X className="mr-1 h-3 w-3" /> 拒绝
          </Button>
        </div>
      ) : (
        <div className={cn(
          "mt-2 text-xs",
          status === "approved" ? "text-emerald-600" : "text-destructive",
        )}>
          {status === "approved" ? "模型已继续执行该工具调用" : "模型已收到拒绝结果"}
        </div>
      )}
    </div>
  );
}

/** DaySeparator marks calendar-day boundaries in long conversations. */
export function DaySeparator({ ts }: { ts: string | undefined }) {
  return (
    <div role="separator" aria-label={dayLabel(ts)} className="flex items-center gap-3 py-1">
      <div className="h-px flex-1 bg-border" aria-hidden />
      <span className="shrink-0 rounded-full bg-muted px-3 py-0.5 text-[11px] text-muted-foreground">{dayLabel(ts)}</span>
      <div className="h-px flex-1 bg-border" aria-hidden />
    </div>
  );
}

// MessageBubble is memoized: streaming appends a new message object for the
// last bubble each token, and every other bubble must keep the same props to
// skip re-rendering. Callers therefore pass stable callbacks and precomputed
// flags instead of per-row closures.
export const MessageBubble = memo(function MessageBubble({ m, isLastAssistant, onRegenerate, canRegenerate, toolRunning, onSpeak, costOf, onEdit, canEdit, onConfirm, durationMs, sessionID }: {
  m: UIMessage;
  isLastAssistant: boolean;
  onRegenerate: () => void;
  canRegenerate: boolean;
  toolRunning: boolean;
  onSpeak: (text: string) => void;
  costOf: (u?: UsageInfo | null) => string;
  onEdit: (text: string) => void;
  canEdit: boolean;
  onConfirm: (msgId: string, callId: string | undefined, approved: boolean) => void;
  durationMs?: number;
  sessionID: string;
}) {
  const [copied, setCopied] = useState(false);
  const copyMsg = async () => {
    try {
      await navigator.clipboard.writeText(m.text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch { /* ignore */ }
  };
  if (m.kind === "user") {
    return (
      <div className="group flex justify-end">
        <div className="flex max-w-[80%] flex-col items-end gap-1.5">
          {(m.attachments?.length ?? 0) > 0 && (
            <div className="flex flex-wrap justify-end gap-1.5">
              {m.attachments!.map((a, i) => (
                <img key={i} src={a.dataUrl} alt={a.name} className="h-20 rounded-lg border object-cover" />
              ))}
            </div>
          )}
          {m.text && (
          <div className="flex items-end gap-1.5">
          {canEdit && (
            <button
              className="mb-1 inline-flex items-center gap-0.5 rounded p-0.5 text-[11px] text-muted-foreground opacity-0 transition-opacity hover:text-foreground group-hover:opacity-100"
              title="编辑并重发这条消息"
              onClick={() => onEdit(m.text)}
            >
              <Pencil className="h-3 w-3" />
              编辑
            </button>
          )}
          <CopyButton text={m.text} className="mb-1 opacity-0 transition-opacity group-hover:opacity-100" />
          <div
            className="whitespace-pre-wrap break-words rounded-2xl rounded-br-md bg-primary px-4 py-2.5 text-sm text-primary-foreground"
            title={fmtTime(m.timestamp)}
          >
            {m.text}
          </div>
          </div>
          )}
        </div>
      </div>
    );
  }
  if (m.kind === "error") {
    return (
      <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive">
        {m.text}
        {canRegenerate && (
          <div className="mt-2">
            <Button
              size="sm"
              variant="outline"
              className="h-7 px-3 text-xs"
              title="裁剪本轮并重试（不重复消耗这条错误）"
              onClick={onRegenerate}
            >
              <RefreshCw className="mr-1 h-3 w-3" /> 重试本轮
            </Button>
          </div>
        )}
      </div>
    );
  }
  if (m.kind === "confirm") {
    return <ConfirmCard m={m} onConfirm={onConfirm} />;
  }
  if (m.kind === "tool_result" && m.toolName === "plan_todos") {
    return <TodoListCard resp={m.toolResp ?? null} />;
  }
  if (m.kind === "tool_call" || m.kind === "tool_result") {
    return (
      <ToolCallCard
        name={m.toolName ?? "tool"}
        sessionID={sessionID}
        args={m.toolArgs}
        resp={m.toolResp}
        running={m.kind === "tool_call" && toolRunning}
        // durationMs comes from the parent's pairing pass; m.durationMs is
        // never populated, so reading it silently hid every timing chip.
        durationMs={durationMs}
      />
    );
  }
  return (
    <div className="group flex justify-start">
      <div className="max-w-[85%]">
        <div className="mb-0.5 flex items-center gap-2">
          <span className="text-[11px] text-muted-foreground">
            {m.author}
            {m.timestamp && <span className="ml-1 opacity-70">· {fmtTime(m.timestamp)}</span>}
          </span>
          {/* Copy/speak/usage work on every finished assistant message —
              scrolling back to grab an earlier reply is a top action. Only
              重新生成 stays bound to the last reply. */}
          {!m.streaming && m.text && (
            <span className="flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100">
              <button
                className="inline-flex items-center gap-0.5 rounded p-0.5 text-[11px] text-muted-foreground hover:text-foreground"
                onClick={copyMsg}
              >
                {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
                {copied ? "已复制" : "复制"}
              </button>
              {isLastAssistant && canRegenerate && (
                <button
                  className="inline-flex items-center gap-0.5 rounded p-0.5 text-[11px] text-muted-foreground hover:text-foreground"
                  title="重新生成最后一条回复"
                  onClick={onRegenerate}
                >
                  <RefreshCw className="h-3 w-3" />
                  重新生成
                </button>
              )}
              <button
                className="inline-flex items-center gap-0.5 rounded p-0.5 text-[11px] text-muted-foreground hover:text-foreground"
                title="朗读这条回复"
                onClick={() => onSpeak(m.text)}
              >
                <Volume2 className="h-3 w-3" />
                朗读
              </button>
              {m.usage && m.usage.totalTokens > 0 && (
                <span className="text-[11px] text-muted-foreground/70" title={`输入 ${m.usage.promptTokens} + 输出 ${m.usage.completionTokens}`}>
                  · {m.usage.totalTokens.toLocaleString()} tokens{costOf(m.usage) && ` · ${costOf(m.usage)}`}
                </span>
              )}
            </span>
          )}
        </div>
        <div className="rounded-2xl rounded-bl-md bg-muted px-4 py-2.5">
          {m.streaming && !m.text ? (
            <span className="inline-flex items-center gap-1 text-sm text-muted-foreground">
              <Loader2 className="h-3 w-3 animate-spin" /> 思考中…
            </span>
          ) : m.kind === "assistant" ? (
            <>
              <MessageMarkdown content={m.text} />
              {m.streaming && <span className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse bg-foreground/60 align-middle" />}
            </>
          ) : (
            <div className="whitespace-pre-wrap break-words text-sm">{m.text}</div>
          )}
        </div>
      </div>
    </div>
  );
});

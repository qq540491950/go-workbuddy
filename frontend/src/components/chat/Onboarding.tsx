import { ArrowRight } from "lucide-react";
import type { SettingsTab } from "@/pages/SettingsPage";

export function Onboarding({ onOpenSettings }: { onOpenSettings: (tab?: SettingsTab) => void }) {
  const steps = [
    { n: 1, title: "配置模型供应商", desc: "添加 OpenAI 兼容 / Anthropic / Gemini 接口" },
    { n: 2, title: "直接开聊", desc: "输入消息立即对话；@ 可指定助手、团队或远程 Agent" },
  ];
  return (
    <div className="mx-auto mt-10 max-w-md rounded-xl border bg-card p-5 shadow-sm">
      <div className="mb-1 text-lg font-semibold">欢迎使用 WorkBuddy Agent 👋</div>
      <p className="mb-4 text-sm text-muted-foreground">基于 Google ADK 的本地 Agent 工作台，两步开始：</p>
      <div className="space-y-3">
        {steps.map((s) => (
          <button
            key={s.n}
            onClick={s.n === 1 ? () => onOpenSettings("models") : undefined}
            className="flex w-full items-center gap-3 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-accent/40"
          >
            <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-sm font-semibold text-primary">
              {s.n}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">{s.title}</span>
              <span className="block text-xs text-muted-foreground">{s.desc}</span>
            </span>
            <ArrowRight className="h-4 w-4 shrink-0 text-muted-foreground" />
          </button>
        ))}
      </div>
    </div>
  );
}

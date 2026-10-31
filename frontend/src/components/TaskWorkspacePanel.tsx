import { useEffect, useMemo, useRef, useState } from "react";
import {
  AlertTriangle, FileImage, FileText, FolderOpen, Loader2, RefreshCw, Search, X,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ArtifactPreview } from "@/components/ToolCallCard";
import { Artifact, errText, type TaskWorkspaceSnapshot, type WorkspaceFileInfo } from "@/lib/api";
import { cn } from "@/lib/utils";

type Tab = "files" | "artifacts" | "changes";

type TaskWorkspacePanelProps = {
  sessionID: string;
  workspace: string;
  open: boolean;
  running: boolean;
  refreshNonce: number;
  onClose: () => void;
};

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function fmtTime(value: string | undefined): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

const statusLabel: Record<string, string> = {
  added: "新增",
  modified: "修改",
  deleted: "删除",
};

function FileRow({ file, onPreview }: {
  file: WorkspaceFileInfo;
  onPreview: (path: string) => void;
}) {
  const icon = file.kind === "image" || file.kind === "pdf"
    ? <FileImage className="h-4 w-4" />
    : <FileText className="h-4 w-4" />;
  return (
    <button
      type="button"
      className={cn(
        "flex w-full items-start gap-2 rounded-lg border bg-card/40 px-2.5 py-2 text-left transition-colors",
        file.previewable && file.status !== "deleted" ? "hover:border-primary/40 hover:bg-accent/40" : "cursor-default",
      )}
      disabled={!file.previewable || file.status === "deleted"}
      onClick={() => file.previewable && file.status !== "deleted" && onPreview(file.path)}
      title={file.previewable && file.status !== "deleted" ? `预览 ${file.path}` : `${file.path}（不支持内嵌预览）`}
      data-file-path={file.path}
    >
      <span className="mt-0.5 shrink-0 text-muted-foreground">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="truncate text-xs font-medium">{file.name}</span>
          {file.status && (
            <Badge
              variant={file.status === "deleted" ? "destructive" : "outline"}
              className="shrink-0 px-1 py-0 text-[10px] font-normal"
            >
              {statusLabel[file.status] ?? file.status}
            </Badge>
          )}
        </span>
        <span className="mt-0.5 block truncate text-[11px] text-muted-foreground" title={file.path}>{file.path}</span>
        <span className="mt-0.5 block text-[10px] text-muted-foreground">
          {fmtSize(file.size)} · {fmtTime(file.modTime)}
          {!file.previewable && " · 需外部打开"}
        </span>
      </span>
    </button>
  );
}

export function TaskWorkspacePanel({ sessionID, workspace, open, running, refreshNonce, onClose }: TaskWorkspacePanelProps) {
  const [snapshot, setSnapshot] = useState<TaskWorkspaceSnapshot | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [tab, setTab] = useState<Tab>("files");
  const [query, setQuery] = useState("");
  const [previewPath, setPreviewPath] = useState<string | null>(null);
  const requestSeq = useRef(0);
  const [manualNonce, setManualNonce] = useState(0);

  useEffect(() => {
    if (!open || !sessionID) return;
    const seq = ++requestSeq.current;
    setLoading(true);
    setError("");
    Artifact.TaskWorkspaceSnapshot(sessionID)
      .then((next) => {
        if (requestSeq.current !== seq) return;
        if (!next) throw new Error("任务结果不可用");
        setSnapshot(next);
      })
      .catch((e) => {
        if (requestSeq.current === seq) setError(errText(e));
      })
      .finally(() => {
        if (requestSeq.current === seq) setLoading(false);
      });
  }, [open, sessionID, refreshNonce, manualNonce]);

  useEffect(() => {
    setTab("files");
    setQuery("");
    setPreviewPath(null);
  }, [sessionID]);

  const files = snapshot?.files ?? [];
  const artifacts = snapshot?.artifacts ?? [];
  const changes = snapshot?.changes ?? [];
  const visibleFiles = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q ? files.filter((f) => f.path.toLowerCase().includes(q)) : files;
  }, [files, query]);

  if (!open || !sessionID) return null;

  const current = tab === "files" ? visibleFiles : tab === "artifacts" ? artifacts : changes;
  const emptyText = tab === "files"
    ? (query.trim() ? "没有匹配的文件" : "任务工作区还没有文件")
    : tab === "artifacts"
      ? "任务完成后，新增或修改的交付文件会显示在这里"
      : "尚未发现新增、修改或删除的文件";

  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/35 lg:hidden" onClick={onClose} aria-hidden />
      <aside
        className="fixed inset-y-0 right-0 z-50 flex w-[min(92vw,420px)] shrink-0 flex-col border-l bg-background shadow-xl lg:static lg:z-auto lg:h-full lg:w-[380px] lg:shadow-none"
        aria-label="任务结果区"
      >
        <div className="flex items-center gap-2 border-b px-3 py-2.5">
          <FolderOpen className="h-4 w-4 shrink-0 text-primary" />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-1.5 text-sm font-medium">
              任务结果
              {running && (
                <Badge variant="secondary" className="gap-1 px-1.5 py-0 text-[10px] font-normal">
                  <Loader2 className="h-2.5 w-2.5 animate-spin" /> 运行中
                </Badge>
              )}
            </div>
            <div className="truncate text-[10px] text-muted-foreground" title={workspace}>{workspace || "未配置工作区"}</div>
          </div>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            className="h-7 w-7"
            title="刷新任务结果"
            aria-label="刷新任务结果"
            disabled={loading}
            onClick={() => setManualNonce((n) => n + 1)}
          >
            <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
          </Button>
          <Button type="button" size="icon" variant="ghost" className="h-7 w-7" aria-label="关闭任务结果区" onClick={onClose}>
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>

        <div className="grid grid-cols-3 border-b px-2" role="tablist" aria-label="任务结果视图">
          {([
            ["files", "全部文件", files.length],
            ["artifacts", "产物", artifacts.length],
            ["changes", "变更", changes.length],
          ] as const).map(([value, label, count]) => (
            <button
              key={value}
              type="button"
              role="tab"
              aria-selected={tab === value}
              className={cn(
                "border-b-2 px-1 py-2 text-xs transition-colors",
                tab === value ? "border-primary font-medium text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
              )}
              onClick={() => setTab(value)}
            >
              {label} {count > 0 && <span className="text-[10px] text-muted-foreground">({count})</span>}
            </button>
          ))}
        </div>

        {tab === "files" && (
          <div className="border-b p-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="按路径搜索文件"
                className="h-8 pl-7 text-xs"
                aria-label="按路径搜索任务文件"
              />
            </div>
          </div>
        )}

        {snapshot?.truncated && (
          <div className="flex items-start gap-2 border-b bg-amber-500/10 px-3 py-2 text-[11px] text-amber-700 dark:text-amber-300">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
            已发现 {snapshot.discovered} 个文件，列表已截断；请缩小工作区范围后刷新。
          </div>
        )}

        <ScrollArea className="min-h-0 flex-1">
          <div className="space-y-1.5 p-2.5">
            {loading && !snapshot && (
              <div className="flex h-40 items-center justify-center gap-2 text-xs text-muted-foreground">
                <Loader2 className="h-4 w-4 animate-spin" /> 正在读取任务工作区…
              </div>
            )}
            {!loading && error && (
              <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-xs text-destructive">
                <div className="mb-1 font-medium">无法读取任务结果</div>
                <div className="break-words">{error}</div>
                <Button size="sm" variant="outline" className="mt-2 h-7" onClick={() => setManualNonce((n) => n + 1)}>
                  重试
                </Button>
              </div>
            )}
            {!loading && !error && current.length === 0 && (
              <div className="py-16 text-center text-xs text-muted-foreground">{emptyText}</div>
            )}
            {!error && current.map((file) => (
              <FileRow
                key={file.path}
                file={file}
                onPreview={setPreviewPath}
              />
            ))}
          </div>
        </ScrollArea>

        {snapshot && (
          <div className="border-t px-3 py-1.5 text-[10px] text-muted-foreground">
            已发现 {snapshot.discovered} 个文件 · 刷新于 {fmtTime(snapshot.refreshedAt)}
          </div>
        )}
        <ArtifactPreview path={previewPath} sessionID={sessionID} onClose={() => setPreviewPath(null)} />
      </aside>
    </>
  );
}

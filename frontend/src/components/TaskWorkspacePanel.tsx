import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { toast } from "sonner";
import {
  AlertTriangle, FileImage, FileText, FolderOpen, Loader2, RefreshCw, Search, X,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ArtifactPreview } from "@/components/ToolCallCard";
import {
  Artifact, errText, WorkspaceChangeStatus,
  WorkspaceFileKind,
  type TaskWorkspaceSnapshot, type WorkspaceFileInfo,
} from "@/lib/api";
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

function fmtTime(value: string | null | undefined): string {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

type ChangeFilter = "all" | WorkspaceChangeStatus;

const statusMeta: Record<WorkspaceChangeStatus, { label: string; className: string }> = {
  [WorkspaceChangeStatus.$zero]: { label: "", className: "" },
  [WorkspaceChangeStatus.ChangeAdded]: { label: "新增", className: "text-emerald-600 dark:text-emerald-400" },
  [WorkspaceChangeStatus.ChangeModified]: { label: "修改", className: "text-amber-600 dark:text-amber-400" },
  [WorkspaceChangeStatus.ChangeDeleted]: { label: "删除", className: "text-destructive" },
};

function FileRow({ file, onPreview }: {
  file: WorkspaceFileInfo;
  onPreview: (path: string) => void;
}) {
  const icon = file.kind === WorkspaceFileKind.FileKindImage || file.kind === WorkspaceFileKind.FileKindPDF
    ? <FileImage className="h-4 w-4" />
    : <FileText className="h-4 w-4" />;
  return (
    <button
      type="button"
      className={cn(
        "flex w-full items-start gap-2 rounded-lg border bg-card/40 px-2.5 py-2 text-left transition-colors",
        file.previewable && file.status !== WorkspaceChangeStatus.ChangeDeleted
          ? "hover:border-primary/40 hover:bg-accent/40"
          : "cursor-default",
      )}
      disabled={!file.previewable || file.status === WorkspaceChangeStatus.ChangeDeleted}
      onClick={() => file.previewable && file.status !== WorkspaceChangeStatus.ChangeDeleted && onPreview(file.path)}
      title={file.previewable && file.status !== WorkspaceChangeStatus.ChangeDeleted
        ? `预览 ${file.path}`
        : `${file.path}（不支持内嵌预览）`}
      data-file-path={file.path}
    >
      <span className="mt-0.5 shrink-0 text-muted-foreground">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="truncate text-xs font-medium">{file.name}</span>
          {file.status && (
            <Badge
              variant={file.status === WorkspaceChangeStatus.ChangeDeleted ? "destructive" : "outline"}
              className={cn("shrink-0 px-1 py-0 text-[10px] font-normal", statusMeta[file.status]?.className)}
            >
              {statusMeta[file.status]?.label || file.status}
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
  const [changeFilter, setChangeFilter] = useState<ChangeFilter>("all");
  const [query, setQuery] = useState("");
  const [previewPath, setPreviewPath] = useState<string | null>(null);
  const requestSeq = useRef(0);
  const [manualNonce, setManualNonce] = useState(0);

  useEffect(() => {
    if (!open || !sessionID) return;
    setSnapshot((prev) => (prev?.sessionId === sessionID ? prev : null));
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
        if (requestSeq.current === seq) {
          const msg = errText(e);
          setError(msg);
          toast.error(`任务结果加载失败: ${msg}`);
        }
      })
      .finally(() => {
        if (requestSeq.current === seq) setLoading(false);
      });
  }, [open, sessionID, refreshNonce, manualNonce]);

  useEffect(() => {
    setTab("files");
    setChangeFilter("all");
    setQuery("");
    setPreviewPath(null);
  }, [sessionID]);

  const activeSnapshot = snapshot?.sessionId === sessionID ? snapshot : null;
  const files = activeSnapshot?.files ?? [];
  const artifacts = activeSnapshot?.artifacts ?? [];
  const changes = activeSnapshot?.changes ?? [];
  const visibleFiles = useMemo(() => {
    const q = query.trim().toLowerCase();
    return q ? files.filter((f) => f.path.toLowerCase().includes(q)) : files;
  }, [files, query]);

  if (!open || !sessionID) return null;

  const filteredChanges = changeFilter === "all"
    ? changes
    : changes.filter((f) => f.status === changeFilter);
  const current = tab === "files" ? visibleFiles : tab === "artifacts" ? artifacts : filteredChanges;
  const emptyText = tab === "files"
    ? (query.trim() ? "没有匹配的文件" : "任务工作区还没有文件")
    : tab === "artifacts"
      ? "任务完成后，新增或修改的交付文件会显示在这里"
      : changeFilter === "all"
        ? "尚未发现新增、修改或删除的文件"
        : `没有${statusMeta[changeFilter].label}的文件`;

  const onTabKeyDown = (e: KeyboardEvent<HTMLButtonElement>, value: Tab) => {
    const order: Tab[] = ["files", "artifacts", "changes"];
    let next = value;
    if (e.key === "ArrowRight") next = order[(order.indexOf(value) + 1) % order.length];
    else if (e.key === "ArrowLeft") next = order[(order.indexOf(value) - 1 + order.length) % order.length];
    else if (e.key === "Home") next = order[0];
    else if (e.key === "End") next = order[order.length - 1];
    else return;
    e.preventDefault();
    setTab(next);
    requestAnimationFrame(() => document.getElementById(`task-tab-${next}`)?.focus());
  };

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
              id={`task-tab-${value}`}
              aria-selected={tab === value}
              aria-controls={tab === value ? `task-panel-${value}` : undefined}
              tabIndex={tab === value ? 0 : -1}
              className={cn(
                "border-b-2 px-1 py-2 text-xs transition-colors",
                tab === value ? "border-primary font-medium text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
              )}
              onClick={() => setTab(value)}
              onKeyDown={(e) => onTabKeyDown(e, value)}
            >
              {label} {count > 0 && <span className="text-[10px] text-muted-foreground">({count})</span>}
            </button>
          ))}
        </div>

        <div
          role="tabpanel"
          id={`task-panel-${tab}`}
          aria-labelledby={`task-tab-${tab}`}
          className="flex min-h-0 flex-1 flex-col"
          aria-live="polite"
        >
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

          {activeSnapshot?.truncated && (
            <div className="flex items-start gap-2 border-b bg-amber-500/10 px-3 py-2 text-[11px] text-amber-700 dark:text-amber-300">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              已发现 {activeSnapshot.discovered} 个文件，列表已截断；请缩小工作区范围后刷新。
            </div>
          )}

          {tab === "changes" && activeSnapshot && (activeSnapshot.added + activeSnapshot.modified + activeSnapshot.deleted > 0) && (
            <div className="flex flex-wrap gap-1.5 border-b px-3 py-2" role="group" aria-label="变更筛选">
              {([
                ["all", "全部", activeSnapshot.added + activeSnapshot.modified + activeSnapshot.deleted],
                [WorkspaceChangeStatus.ChangeAdded, "新增", activeSnapshot.added],
                [WorkspaceChangeStatus.ChangeModified, "修改", activeSnapshot.modified],
                [WorkspaceChangeStatus.ChangeDeleted, "删除", activeSnapshot.deleted],
              ] as const).map(([value, label, count]) => (
                <button
                  key={value}
                  type="button"
                  aria-pressed={changeFilter === value}
                  className={cn(
                    "rounded-full border px-2 py-0.5 text-[10px] transition-colors",
                    changeFilter === value ? "border-primary bg-primary/10 text-foreground" : "text-muted-foreground hover:text-foreground",
                  )}
                  onClick={() => setChangeFilter(value)}
                >
                  {label} {count}
                </button>
              ))}
            </div>
          )}

          <ScrollArea className="min-h-0 flex-1">
            <div className="space-y-1.5 p-2.5">
              {loading && !activeSnapshot && (
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

          {activeSnapshot && (
            <div className="border-t px-3 py-1.5 text-[10px] text-muted-foreground">
              已发现 {activeSnapshot.discovered} 个文件 · 基线 {fmtTime(activeSnapshot.baselineAt)} · 刷新于 {fmtTime(activeSnapshot.refreshedAt)}
            </div>
          )}
        </div>
        <ArtifactPreview path={previewPath} sessionID={sessionID} onClose={() => setPreviewPath(null)} />
      </aside>
    </>
  );
}

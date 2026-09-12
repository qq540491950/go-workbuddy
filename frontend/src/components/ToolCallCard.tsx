import { useEffect, useMemo, useState } from "react";
import {
  CheckCircle2, ChevronRight, FileImage, FileText, Loader2, XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { MessageMarkdown } from "@/components/MessageMarkdown";
import { Artifact, errText, type Artifact as ArtifactData } from "@/lib/api";

/** extractArtifactPaths finds workspace-relative file references in tool
 *  arguments/results so their outputs can be previewed. */
export function extractArtifactPaths(...objs: (unknown | null | undefined)[]): string[] {
  const out = new Set<string>();
  const re = /"(?:path|file|file_path|filename|image_path|artifact|output)"\s*:\s*"([^"]+\.(?:png|jpe?g|gif|webp|svg|bmp|ico|pdf|md|markdown|txt|log|csv|json|ya?ml|xml|html?|go|ts|tsx|js|py|rs|java|sh|sql|toml|ini|css))"/gi;
  for (const o of objs) {
    if (o == null) continue;
    try {
      for (const m of JSON.stringify(o).matchAll(re)) out.add(m[1]);
    } catch { /* ignore */ }
  }
  return [...out];
}

type ToolCallProps = {
  name: string;
  args?: { [k: string]: any } | null;
  resp?: { [k: string]: any } | null;
  running: boolean;
};

/** ToolCallCard renders one tool invocation with live status visualization. */
export function ToolCallCard({ name, args, resp, running }: ToolCallProps) {
  const [open, setOpen] = useState(false);
  const failed = useMemo(() => {
    if (!resp) return false;
    const s = JSON.stringify(resp).toLowerCase();
    return s.includes('"error"') || s.includes('"is_error":true');
  }, [resp]);

  const argChips = useMemo(() => {
    if (!args) return [];
    return Object.entries(args).slice(0, 4).map(([k, v]) => {
      let text = typeof v === "string" ? v : JSON.stringify(v);
      if (text.length > 42) text = text.slice(0, 42) + "…";
      return { k, text };
    });
  }, [args]);

  const paths = useMemo(
    () => extractArtifactPaths(args, resp),
    [args, resp],
  );

  return (
    <div className="rounded-lg border bg-muted/30 text-sm transition-colors" data-toolcard={name}>
      <button
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
        onClick={() => setOpen((v) => !v)}
      >
        <span className="shrink-0">
          {running ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" />
          ) : failed ? (
            <XCircle className="h-3.5 w-3.5 text-destructive" />
          ) : (
            <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />
          )}
        </span>
        <Badge variant="outline" className="shrink-0 font-normal">
          {running ? "调用中" : failed ? "失败" : "完成"}
        </Badge>
        <span className="font-mono text-xs">{name}</span>
        <span className="hidden min-w-0 flex-1 items-center gap-1 overflow-hidden sm:flex">
          {argChips.map(({ k, text }) => (
            <span key={k} className="truncate rounded bg-background px-1.5 py-0.5 text-[11px] text-muted-foreground" title={`${k}: ${text}`}>
              {text}
            </span>
          ))}
        </span>
        <ChevronRight className={`ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`} />
      </button>
      {open && (
        <div className="border-t">
          <pre className="max-h-44 overflow-auto px-3 py-2 text-xs text-muted-foreground">
            {JSON.stringify(running ? args : (resp ?? args), null, 2)}
          </pre>
        </div>
      )}
      {paths.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 border-t px-3 py-1.5">
          <span className="text-[11px] text-muted-foreground">产物：</span>
          {paths.map((p) => (
            <ArtifactOpenButton key={p} path={p} />
          ))}
        </div>
      )}
    </div>
  );
}

export function ArtifactOpenButton({ path, label }: { path: string; label?: string }) {
  const [target, setTarget] = useState<string | null>(null);
  return (
    <>
      <Button
        variant="outline"
        size="sm"
        className="h-6 gap-1 px-2 text-[11px]"
        onClick={() => setTarget(path)}
      >
        <FileImage className="h-3 w-3" />
        {label ?? "预览"} {path.split("/").pop()}
      </Button>
      <ArtifactPreview path={target} onClose={() => setTarget(null)} />
    </>
  );
}

const kindIcon = (kind: string) =>
  kind === "image" ? <FileImage className="h-4 w-4" /> : <FileText className="h-4 w-4" />;

/** ArtifactPreview shows workspace files: images, PDF, markdown and text. */
export function ArtifactPreview({ path, onClose }: { path: string | null | undefined; onClose: () => void }) {
  const [art, setArt] = useState<ArtifactData | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!path) {
      setArt(null);
      setError("");
      return;
    }
    setLoading(true);
    Artifact.Open(path)
      .then((a) => {
        if (!a) throw new Error("未找到文件");
        setArt(a);
      })
      .catch((e) => setError(errText(e)))
      .finally(() => setLoading(false));
  }, [path]);

  return (
    <Dialog open={!!path} onOpenChange={(v) => !v && onClose()}>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-base">
            {art && kindIcon(art.kind)}
            <span className="min-w-0 truncate">{art?.name ?? path}</span>
            {art && (
              <Badge variant="outline" className="font-normal">
                {art.kind}
              </Badge>
            )}
            {art?.truncated && (
              <Badge variant="secondary" className="font-normal">
                已截断
              </Badge>
            )}
          </DialogTitle>
        </DialogHeader>
        <div className="min-h-0 flex-1 overflow-auto rounded-lg border bg-background p-3">
          {loading && (
            <div className="flex h-40 items-center justify-center text-sm text-muted-foreground">
              <Loader2 className="mr-2 h-4 w-4 animate-spin" /> 加载中…
            </div>
          )}
          {!loading && error && (
            <div className="text-sm text-destructive">{error}</div>
          )}
          {!loading && !error && art?.kind === "image" && (
            <div className="flex justify-center">
              <img src={art.dataUrl} alt={art.name} className="max-h-[60vh] max-w-full rounded object-contain" />
            </div>
          )}
          {!loading && !error && art?.kind === "pdf" && (
            <iframe src={art.dataUrl ?? ""} title={art.name} className="h-[60vh] w-full rounded" />
          )}
          {!loading && !error && art?.kind === "markdown" && (
            <MessageMarkdown content={art.text ?? ""} />
          )}
          {!loading && !error && art?.kind === "text" && (
            <pre className="whitespace-pre-wrap font-mono text-xs leading-relaxed">{art.text}</pre>
          )}
        </div>
        {art && (
          <div className="pt-2 text-right text-xs text-muted-foreground">
            工作区路径: {art.path} · {(art.size / 1024).toFixed(1)} KB
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}

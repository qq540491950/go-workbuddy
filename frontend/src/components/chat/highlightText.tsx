import type { ReactNode } from "react";

// highlightText renders text with case-insensitive occurrences of query
// marked. Only used on plain-text render paths (user bubbles, errors, tool
// JSON); assistant markdown is deliberately not touched. Empty query returns
// the text untouched so the call sites stay unconditional.
export function highlightText(text: string, query: string, markClass: string): ReactNode {
  if (!query || !text) return text;
  const lower = text.toLowerCase();
  const q = query.toLowerCase();
  const out: ReactNode[] = [];
  let i = 0;
  let key = 0;
  while (i < text.length) {
    const hit = lower.indexOf(q, i);
    if (hit < 0) {
      out.push(text.slice(i));
      break;
    }
    if (hit > i) out.push(text.slice(i, hit));
    out.push(<mark key={key++} className={markClass}>{text.slice(hit, hit + q.length)}</mark>);
    i = hit + q.length;
  }
  return out;
}

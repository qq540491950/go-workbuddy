/** parseEnvText turns the editor's "KEY=VALUE" lines into a config map,
 * splitting at the FIRST '=' only: values routinely contain '=' (base64
 * padding, connection strings) and split("=", 2) used to corrupt them. */
export function parseEnvText(text: string): Record<string, string> {
  return Object.fromEntries(
    text
      .split("\n")
      .map((l) => {
        const i = l.indexOf("=");
        return i > 0 ? [l.slice(0, i).trim(), l.slice(i + 1)] : null;
      })
      .filter((p): p is [string, string] => !!p && p[0].trim().length > 0),
  );
}

/** envToText is the inverse of parseEnvText for the editor's textarea.
 * Note: values containing newlines cannot be represented in the line-based
 * editor (imported configs could hold them) — the tail after the first
 * newline is dropped by parseEnvText on save. */
export function envToText(env: Record<string, string> | undefined | null): string {
  return Object.entries(env ?? {})
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");
}

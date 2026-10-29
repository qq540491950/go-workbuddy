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

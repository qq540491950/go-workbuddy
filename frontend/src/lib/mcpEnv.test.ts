import { describe, expect, it } from "vitest";
import { envToText, parseEnvText } from "./mcpEnv";

describe("parseEnvText", () => {
  it("keeps '=' inside values (base64 padding, connection strings)", () => {
    expect(parseEnvText("TOKEN=abc=def==\nURL=postgres://u:p@h/db")).toEqual({
      TOKEN: "abc=def==",
      URL: "postgres://u:p@h/db",
    });
  });
  it("drops lines without a key or a separator", () => {
    expect(parseEnvText("=nokey\nKEY=ok\nnoseparator\n  \nKEY2 = spaced")).toEqual({
      KEY: "ok",
      KEY2: " spaced",
    });
  });
});

describe("envToText round-trip", () => {
  it("round-trips newline-free values losslessly", () => {
    const env = { TOKEN: "abc=def==", URL: "postgres://u:p@h/db" };
    expect(parseEnvText(envToText(env))).toEqual(env);
  });
  it("documents the newline-in-value limitation", () => {
    // A value holding a newline cannot survive the line-based editor: the
    // tail becomes an orphan line and is dropped. Imported configs only.
    const env = { A: "line1\nline2" };
    expect(parseEnvText(envToText(env))).toEqual({ A: "line1" });
  });
});

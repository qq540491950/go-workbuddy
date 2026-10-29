import { describe, expect, it } from "vitest";
import { parseEnvText } from "./mcpEnv";

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

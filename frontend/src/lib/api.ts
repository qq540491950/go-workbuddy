// Thin re-export layer over the generated Wails bindings.
import * as services from "../../bindings/changeme/internal/services";
import * as configModels from "../../bindings/changeme/internal/config/models";
import * as mcpModels from "../../bindings/changeme/internal/mcpmgr/models";
import * as serviceModels from "../../bindings/changeme/internal/services/models";

export const App = services.AppService;
export const Chat = services.ChatService;
export const Config = services.ConfigService;
export const MCP = services.MCPService;
export const Artifact = services.ArtifactService;

export const Protocol = configModels.Protocol;
export type Protocol = configModels.Protocol;
export const MCPTransport = configModels.MCPTransport;
export type MCPTransport = configModels.MCPTransport;

export type UsageInfo = serviceModels.UsageInfo;
export type Artifact = serviceModels.Artifact;
export type ArtifactInfo = serviceModels.ArtifactInfo;
export type AttachmentIn = serviceModels.AttachmentIn;
export type AttachmentOut = serviceModels.AttachmentOut;
export type SessionStats = serviceModels.SessionStats;
export type ChatMessage = serviceModels.ChatMessage;
export type ChatStreamEvent = serviceModels.ChatStreamEvent;
export type ServerStatus = mcpModels.ServerStatus;
export type ToolInfo = mcpModels.ToolInfo;

export type AppConfig = configModels.Config;
export type ModelProvider = configModels.ModelProvider;
export type AgentConfig = configModels.AgentConfig;
export type TeamConfig = configModels.TeamConfig;
export type SkillConfig = configModels.SkillConfig;
export type MCPServerConfig = configModels.MCPServerConfig;
export type Settings = configModels.Settings;
export type ChatSession = configModels.ChatSession;


/** errText extracts a human-readable message from any binding failure. */
export function errText(e: unknown): string {
  if (!e) return "未知错误";
  const anyE = e as { message?: string; cause?: unknown };
  if (anyE.message && anyE.message.trim()) return anyE.message;
  if (anyE.cause != null) return errText(anyE.cause);
  if (typeof e === "string" && e.trim()) return e;
  try { return JSON.stringify(e); } catch { return String(e); }
}

/** uid makes a client-side unique id for streaming message keys. */
let uidCounter = 0;
export function uid(prefix = "u"): string {
  uidCounter += 1;
  return `${prefix}-${Date.now()}-${uidCounter}`;
}

(function () {
  if (window.__mockInstalled) return;
  var now = new Date().toISOString();
  var ws = "/Users/liruitao/Documents/WorkBuddyWorkspace";
  var cfg = {
    settings: { workspaceDir: ws, language: "zh-CN", themeMode: "dark", accent: "blue", fontScale: "md", autoSpeak: false },
    providers: [{ id: "prov_1", name: "DeepSeek", protocol: "openai", baseUrl: "https://api.deepseek.com/v1", apiKey: "sk-demo", models: ["deepseek-chat"], isDefault: true, priceIn: 2, priceOut: 8 }],
    agents: [
      { id: "agent_1", name: "周报助手", description: "周报", providerId: "prov_1", model: "deepseek-chat", systemPrompt: "", temperature: 0.7, maxTokens: 4096, skillIds: [], mcpServerIds: [], builtinTools: ["time"], createdAt: now },
      { id: "agent_2", name: "代码审查员", description: "审查代码", providerId: "prov_1", model: "deepseek-chat", systemPrompt: "", temperature: 0.3, maxTokens: 4096, skillIds: [], mcpServerIds: [], builtinTools: [], createdAt: now },
      { id: "agent_3", name: "配图设计师", description: "为文章配图", providerId: "prov_1", model: "deepseek-chat", systemPrompt: "", temperature: 0.7, maxTokens: 4096, skillIds: [], mcpServerIds: [], builtinTools: [], createdAt: now }
    ],
    teams: [{ id: "team_1", name: "内容创作团队", description: "写作+审查流水线", leadAgentId: "agent_1", autoCoordinate: false, memberAgentIds: ["agent_1", "agent_2", "agent_3"], providerId: "prov_1", model: "deepseek-chat", systemPrompt: "", createdAt: now }],
    skills: [], mcpServers: [],
    sessions: [{ id: "sess_1", title: "成本与编辑测试", targetType: "agent", targetId: "agent_1", createdAt: now, updatedAt: now }]
  };
  var msgs = {
    sess_1: [
      { id: "m1", kind: "user", author: "user", text: "看看这张截图里的数据。", timestamp: now, attachments: [{ name: "shot.png", mime: "image/png", dataUrl: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAgAAAAICAYAAADED76LAAAAF0lEQVR42mNkYPjPwMDAwMgABXAGACwBA/+8kUOnAAAAAElFTkSuQmCC" }] },
      { id: "m2", kind: "assistant", author: "weekly_assistant", text: "本周完成 **12 项**任务，环比增长 10%。", timestamp: now }
    ]
  };
  function upsert(list, item, prefix) {
    if (!item.id) item.id = prefix + Date.now();
    var i = list.findIndex(function (x) { return x.id === item.id; });
    if (i >= 0) list[i] = item; else list.push(item);
  }
  var H = {
    362021961: function () { return cfg; },
    488827929: function (p) { upsert(cfg.providers, p, "prov_"); },
    3002230555: function (id) { cfg.providers = cfg.providers.filter(function (x) { return x.id !== id; }); },
    1354150776: function () { throw new Error("连接失败: HTTP 401 Unauthorized"); },
    482075013: function (a) { upsert(cfg.agents, a, "agent_"); },
    3938069995: function (id) { cfg.agents = cfg.agents.filter(function (x) { return x.id !== id; }); },
    2557233981: function (t) { upsert(cfg.teams, t, "team_"); },
    661470679: function (id) { cfg.teams = cfg.teams.filter(function (x) { return x.id !== id; }); },
    3441334657: function (s) { upsert(cfg.skills, s, "skill_"); },
    3210932935: function (id) { cfg.skills = cfg.skills.filter(function (x) { return x.id !== id; }); },
    2532663851: function (s) { upsert(cfg.mcpServers, s, "mcp_"); },
    22452925: function (id) { cfg.mcpServers = cfg.mcpServers.filter(function (x) { return x.id !== id; }); },
    2460420529: function (s) { cfg.settings = s; },
    1810596567: function () { return { name: "WorkBuddy Agent", version: "0.2.0", adk: "google adk-go v2" }; },
    1516353476: function () { return cfg.sessions; },
    3793260767: function (type, id, title) {
      var s = { id: "sess_" + Date.now(), title: title || "新对话", targetType: type, targetId: id, createdAt: now, updatedAt: now };
      cfg.sessions.unshift(s); msgs[s.id] = []; return s;
    },
    3662319581: function (sid) { return msgs[sid] || []; },
    1748553651: function (sid, text) {
      (msgs[sid] = msgs[sid] || []).push({ id: "u" + Date.now(), kind: "user", author: "user", text: text, timestamp: now });
      var s = cfg.sessions.find(function (x) { return x.id === sid; });
      if (s && s.title === "新对话") s.title = text.slice(0, 24);
      msgs[sid].push({ id: "a" + Date.now(), kind: "assistant", author: "assistant", text: "收到：" + text, timestamp: now });
    },
    4065508909: function (sid, text) {
      var arr = msgs[sid] || [];
      while (arr.length > 0 && (arr[arr.length-1].kind === "assistant" || arr[arr.length-1].kind === "tool_call" || arr[arr.length-1].kind === "tool_result")) arr.pop();
      if (arr.length > 0 && arr[arr.length-1].kind === "user") arr[arr.length-1].text = text;
      else arr.push({ id: "u" + Date.now(), kind: "user", author: "user", text: text, timestamp: now });
      arr.push({ id: "a" + Date.now(), kind: "assistant", author: "assistant", text: "编辑后收到：**" + text.slice(0, 16) + "**", timestamp: now });
    },
    368507619: function (sid) { var arr = msgs[sid] || []; while (arr.length && arr[arr.length-1].kind === "assistant") arr.pop(); },
    3243786305: function (sid) { return ws + "/exports/" + sid + ".md"; },
    3574706456: function () { return false; },
    3046108191: function () {},
    3416606985: function (id, title) { var s = cfg.sessions.find(function (x) { return x.id === id; }); if (s) s.title = title; },
    2720233066: function (id) { cfg.sessions = cfg.sessions.filter(function (x) { return x.id !== id; }); delete msgs[id]; },
    1089169061: function () { return []; },
    3458547111: function (id) { return { id: id, name: "", state: "error", error: "mock", checkedAt: now }; },
    1232363321: function () {},
    2472935809: function (p) {
      if (p.indexOf(".md") !== -1) return { name: "report.md", path: p, kind: "markdown", mime: "text/markdown", size: 128, text: "# 文档\n内容" };
      return { name: "img.png", path: p, kind: "image", mime: "image/png", size: 96, dataUrl: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==" };
    },
    2323643321: function () { return []; },
    3216171198: function () { return ws; }
  };
  var orig = window.fetch.bind(window);
  window.fetch = function (input, init) {
    var url = typeof input === "string" ? input : (input && input.url) ? input.url : String(input);
    if (url && url.indexOf("/wails/runtime") !== -1 && init && init.method === "POST") {
      var body = JSON.parse(init.body);
      var inner = body && body.args && typeof body.args === "object" ? body.args : body;
      var handler = H[inner.methodID];
      return new Promise(function (resolve) {
        setTimeout(function () {
          if (!handler) {
            resolve(new Response(JSON.stringify({ kind: "ReferenceError", message: "unknown method " + inner.methodID }), { status: 400, headers: { "Content-Type": "application/json" } }));
            return;
          }
          try {
            var out = handler.apply(null, inner.args || []);
            resolve(new Response(JSON.stringify(out === undefined ? null : out), { status: 200, headers: { "Content-Type": "application/json" } }));
          } catch (e) {
            resolve(new Response(JSON.stringify({ kind: "RuntimeError", message: String(e.message || e) }), { status: 400, headers: { "Content-Type": "application/json" } }));
          }
        }, 100);
      });
    }
    return orig(input, init);
  };
  window.__mockInstalled = true;
})();

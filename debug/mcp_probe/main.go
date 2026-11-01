// Debug probe: load the real WorkBuddy config (read-only) and connect every
// configured MCP server exactly like main.go's startup auto-connect, to tell
// apart "old binary still running" from "injection path broken". Env values
// and tokens are never printed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go-workbuddy/internal/config"
	"go-workbuddy/internal/mcpmgr"
)

func main() {
	base, err := os.UserConfigDir()
	if err != nil {
		fmt.Println("user config dir:", err)
		os.Exit(1)
	}
	store, err := config.Open(filepath.Join(base, "workbuddy-agent"))
	if err != nil {
		fmt.Println("open config:", err)
		os.Exit(1)
	}
	mgr := mcpmgr.New()
	for _, s := range store.Get().MCPServers {
		st := mgr.Connect(s)
		out, _ := json.MarshalIndent(map[string]any{
			"name":     st.Name,
			"command":  s.Command,
			"args":     s.Args,
			"disabled": s.Disabled,
			"state":    st.State,
			"tools":    st.Tools,
			"error":    st.Error,
		}, "", "  ")
		fmt.Println(string(out))
	}
}

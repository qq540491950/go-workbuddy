package services

import "testing"

// AppInfo must expose the version injected from main (single source of truth).
func TestAppInfoReportsInjectedVersion(t *testing.T) {
	AppVersion = "9.9.9-test"
	defer func() { AppVersion = "dev" }()
	a := NewAppService(&Services{})
	info := a.AppInfo()
	if info["version"] != "9.9.9-test" || info["name"] != "WorkBuddy Agent" {
		t.Fatalf("AppInfo = %v", info)
	}
}

package sign

import (
	"context"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestSignableWindows(t *testing.T) {
	arts := []plugin.Artifact{
		{Path: "/d/App Setup 1.0.0.exe"},
		{Path: "/d/App-1.0.0.msi"},
		{Path: "/d/App-1.0.0.dmg"},      // mac — not signable here
		{Path: "/d/App-1.0.0.AppImage"}, // linux — not signable here
		{Path: "/d/latest.yml"},         // updater manifest — not signable
	}
	got := SignableWindows(arts)
	if len(got) != 2 {
		t.Fatalf("expected 2 signable (exe, msi), got %d: %+v", len(got), got)
	}
}

func TestWindowsCommandsAzure(t *testing.T) {
	cfg := &config.WindowsSigning{
		Endpoint:    "https://wus2.codesigning.azure.net",
		Account:     "myacct",
		CertProfile: "myprofile",
	}
	cmds, err := WindowsCommands(cfg, []string{"/out/App Setup.exe"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 {
		t.Fatalf("want 1 command, got %d", len(cmds))
	}
	got := strings.Join(cmds[0], " ")
	for _, want := range []string{
		"sign code trusted-signing",
		"-b /out",
		"-tse https://wus2.codesigning.azure.net",
		"-tsa myacct",
		"-tscp myprofile",
		"-t http://timestamp.acs.microsoft.com", // default timestamp
		"App Setup.exe",                         // basename positional
	} {
		if !strings.Contains(got, want) {
			t.Errorf("command %q missing %q", got, want)
		}
	}
}

func TestWindowsCommandsAzureMissingCoordinates(t *testing.T) {
	_, err := WindowsCommands(&config.WindowsSigning{Endpoint: "https://x"}, []string{"/o/a.exe"})
	if err == nil {
		t.Fatal("expected an error when account/certificateProfile are missing")
	}
}

func TestWindowsCommandsEscapeHatch(t *testing.T) {
	cfg := &config.WindowsSigning{Tool: "command", Command: []string{"AzureSignTool", "sign", "-kvu", "https://kv", "{file}"}}
	cmds, err := WindowsCommands(cfg, []string{"/o/a.exe", "/o/b.msi"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 || cmds[0][len(cmds[0])-1] != "/o/a.exe" || cmds[1][len(cmds[1])-1] != "/o/b.msi" {
		t.Errorf("{file} substitution wrong: %+v", cmds)
	}
}

func TestWindowsCommandsUnknownTool(t *testing.T) {
	_, err := WindowsCommands(&config.WindowsSigning{Tool: "magic"}, []string{"/o/a.exe"})
	if err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
}

func TestSignWindowsDryRun(t *testing.T) {
	arts := []plugin.Artifact{{Path: "/out/App.exe"}, {Path: "/out/App.dmg"}}
	cfg := &config.WindowsSigning{Endpoint: "https://e", Account: "a", CertProfile: "p"}
	signed, output, err := SignWindows(context.Background(), arts, cfg, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(signed) != 1 || signed[0] != "/out/App.exe" {
		t.Errorf("dry-run should report the .exe as would-sign, got %v", signed)
	}
	if !strings.Contains(output, "would sign App.exe") {
		t.Errorf("dry-run output missing preview: %q", output)
	}
}

func TestSignWindowsNoSignableIsNoop(t *testing.T) {
	arts := []plugin.Artifact{{Path: "/out/App.dmg"}, {Path: "/out/App.AppImage"}}
	signed, output, err := SignWindows(context.Background(), arts, &config.WindowsSigning{Endpoint: "e", Account: "a", CertProfile: "p"}, nil, nil, false)
	if err != nil || len(signed) != 0 || output != "" {
		t.Errorf("no Windows artifacts should be a no-op, got signed=%v output=%q err=%v", signed, output, err)
	}
}

func TestMergeEnv(t *testing.T) {
	base := []string{"PATH=/x"}
	got := mergeEnv(base, map[string]string{"AZURE_CLIENT_ID": "id", "AZURE_TENANT_ID": "t"})
	want := "PATH=/x,AZURE_CLIENT_ID=id,AZURE_TENANT_ID=t"
	if strings.Join(got, ",") != want {
		t.Errorf("mergeEnv = %v, want %s", got, want)
	}
}

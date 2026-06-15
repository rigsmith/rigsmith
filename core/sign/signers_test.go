package sign

import (
	"context"
	"strings"
	"testing"

	"github.com/rigsmith/rigsmith/core/config"
	"github.com/rigsmith/rigsmith/core/plugin"
)

func TestMatchesSigner(t *testing.T) {
	s := config.Signer{Extensions: []string{".exe", ".MSI"}} // mixed case on purpose
	for path, want := range map[string]bool{
		"/d/App Setup 1.0.0.exe": true,
		"/d/App-1.0.0.msi":       true, // case-insensitive
		"/d/App-1.0.0.dmg":       false,
		"/d/latest.yml":          false,
	} {
		if got := matchesSigner(s, path); got != want {
			t.Errorf("matchesSigner(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestSignerCommandsAzure(t *testing.T) {
	s := config.Signer{
		Tool:        "azure-trusted-signing",
		Endpoint:    "https://wus2.codesigning.azure.net",
		Account:     "myacct",
		CertProfile: "myprofile",
	}
	cmds, err := SignerCommands(s, []string{"/out/App Setup.exe"})
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

func TestSignerCommandsAzureMissingCoordinates(t *testing.T) {
	_, err := SignerCommands(config.Signer{Tool: "azure-trusted-signing", Endpoint: "https://x"}, []string{"/o/a.exe"})
	if err == nil {
		t.Fatal("expected an error when account/certificateProfile are missing")
	}
}

func TestSignerCommandsCommand(t *testing.T) {
	// Default tool is "command"; "{file}" is substituted per file.
	s := config.Signer{Command: []string{"rcodesign", "sign", "{file}"}}
	cmds, err := SignerCommands(s, []string{"/o/App.dmg", "/o/App.app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 2 || cmds[0][2] != "/o/App.dmg" || cmds[1][2] != "/o/App.app" {
		t.Errorf("{file} substitution wrong: %+v", cmds)
	}
}

func TestSignerCommandsCommandNeedsArgv(t *testing.T) {
	if _, err := SignerCommands(config.Signer{Tool: "command"}, []string{"/o/a.dmg"}); err == nil {
		t.Fatal("expected an error for a command signer with no command")
	}
}

func TestSignerCommandsUnknownTool(t *testing.T) {
	if _, err := SignerCommands(config.Signer{Tool: "magic"}, []string{"/o/a.exe"}); err == nil {
		t.Fatal("expected an error for an unknown tool")
	}
}

func TestApplyDryRunMultiPlatform(t *testing.T) {
	arts := []plugin.Artifact{
		{Path: "/out/App.exe"},
		{Path: "/out/App.dmg"},
		{Path: "/out/App.AppImage"}, // unmatched by either signer
	}
	signers := []config.Signer{
		{Extensions: []string{".exe"}, Tool: "azure-trusted-signing", Endpoint: "https://e", Account: "a", CertProfile: "p"},
		{Extensions: []string{".dmg"}, Command: []string{"rcodesign", "sign", "{file}"}},
	}
	signed, output, err := Apply(context.Background(), arts, signers, nil, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(signed) != 2 {
		t.Fatalf("expected exe + dmg previewed, got %v", signed)
	}
	if !strings.Contains(output, "sign code trusted-signing") || !strings.Contains(output, "rcodesign sign /out/App.dmg") {
		t.Errorf("dry-run preview missing a signer: %q", output)
	}
	if strings.Contains(output, "AppImage") {
		t.Errorf("unmatched .AppImage should not be signed: %q", output)
	}
}

func TestApplyNoSignersIsNoop(t *testing.T) {
	signed, output, err := Apply(context.Background(), []plugin.Artifact{{Path: "/o/a.exe"}}, nil, nil, nil, false)
	if err != nil || len(signed) != 0 || output != "" {
		t.Errorf("no signers should be a no-op, got signed=%v output=%q err=%v", signed, output, err)
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

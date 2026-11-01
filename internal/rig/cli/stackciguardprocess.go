package cli

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func stackGuardCommand(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "dotnet", args...)
	command.Env = append(os.Environ(), "DOTNET_CLI_UI_LANGUAGE=en-US", "VSLANG=1033", "DOTNET_NOLOGO=1")
	return command
}

func stackGuardEvaluate(command *exec.Cmd) ([]byte, error) {
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.Output()
	if err != nil {
		return stdout, fmt.Errorf("%w: %s", err, stderr.String())
	}
	return stdout, nil
}

// The hook exists only for this build. References remove it together with the
// entry project's lists, so their own hooks and warning policies evaluate anew.
func stackGuardPlainTargets(original, toolsPath string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "rig-stack-guard-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if original != "" && !filepath.IsAbs(original) {
		original = filepath.Join(toolsPath, original)
	}
	var body bytes.Buffer
	body.WriteString("<Project>\n")
	if original != "" {
		var escaped bytes.Buffer
		if err := xml.EscapeText(&escaped, []byte(original)); err != nil {
			cleanup()
			return "", nil, err
		}
		fmt.Fprintf(&body, "  <PropertyGroup><_RigStackOriginalHook>%s</_RigStackOriginalHook></PropertyGroup>\n", escaped.String())
		body.WriteString(`  <Import Project="$(_RigStackOriginalHook)" Condition="Exists('$(_RigStackOriginalHook)')" />
`)
	}
	body.WriteString(`  <Target Name="RigStackCIPlainReferences" BeforeTargets="AssignProjectConfiguration;_GenerateRestoreProjectPathWalk">
    <ItemGroup>
      <ProjectReference Update="@(ProjectReference)">
        <GlobalPropertiesToRemove>%(ProjectReference.GlobalPropertiesToRemove);WarningsAsErrors;WarningsNotAsErrors;CustomAfterMicrosoftCommonTargets</GlobalPropertiesToRemove>
      </ProjectReference>
    </ItemGroup>
  </Target>
</Project>
`)
	path := filepath.Join(dir, "Guard.targets")
	if err := os.WriteFile(path, body.Bytes(), 0600); err != nil {
		cleanup()
		return "", nil, err
	}
	return path, cleanup, nil
}

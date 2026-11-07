package plugin

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestArtifactsPropertiesWireCompatibility(t *testing.T) {
	// The request envelope before Properties was added.
	type oldRequest struct {
		APIVersion int     `json:"apiVersion"`
		RepoRoot   string  `json:"repoRoot"`
		Package    Package `json:"package"`
		OutputDir  string  `json:"outputDir"`
	}
	old := oldRequest{APIVersion: APIVersion, Package: Package{Name: "Lib", Version: "1.2.3"}}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	var current ArtifactsRequest
	if err := json.Unmarshal(data, &current); err != nil || current.Properties != nil {
		t.Fatalf("old host request: %+v, %v", current, err)
	}
	for _, properties := range []map[string]string{nil, {}} {
		current.Properties = properties
		data, err = json.Marshal(current)
		if err != nil || strings.Contains(string(data), `"properties"`) {
			t.Fatalf("empty properties changed wire envelope: %s, %v", data, err)
		}
	}
	current.Properties = map[string]string{"CustomAfterMicrosoftCommonTargets": "a path/edge.targets"}
	data, err = json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	var legacy oldRequest
	if err := json.Unmarshal(data, &legacy); err != nil || !reflect.DeepEqual(legacy, old) {
		t.Fatalf("old plugin must ignore additive properties: %+v, %v", legacy, err)
	}
	var roundTrip ArtifactsRequest
	if err := json.Unmarshal(data, &roundTrip); err != nil || !reflect.DeepEqual(roundTrip.Properties, current.Properties) {
		t.Fatalf("properties lost on wire: %+v, %v", roundTrip, err)
	}
}

func TestArtifactsPropertiesCapability(t *testing.T) {
	req := ArtifactsRequest{Properties: map[string]string{"PackageId": "You.Lib"}}
	info := EcosystemInfo{ID: "legacy", Capabilities: []string{MethodArtifacts}}
	if err := req.CheckProperties(info); err == nil || !strings.Contains(err.Error(), CapabilityArtifactsProperties) {
		t.Fatalf("missing capability: %v", err)
	}
	info.Capabilities = append(info.Capabilities, CapabilityArtifactsProperties)
	if err := req.CheckProperties(info); err != nil {
		t.Fatal(err)
	}
	if err := (ArtifactsRequest{}).CheckProperties(EcosystemInfo{ID: "legacy"}); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactsPropertiesSubprocessRefusesBeforeDispatch(t *testing.T) {
	// No host: dispatch to the old plugin would panic. It could ignore the new
	// field, so the host must refuse before invoking it.
	eco := &SubprocessEcosystem{info: EcosystemInfo{ID: "old", Capabilities: []string{MethodArtifacts}}}
	_, err := eco.Artifacts(context.Background(), ArtifactsRequest{Properties: map[string]string{"PackageId": "You.Lib"}})
	if err == nil || !strings.Contains(err.Error(), CapabilityArtifactsProperties) {
		t.Fatalf("missing-capability refusal: %v", err)
	}
}

func TestArtifactsPropertiesSubprocessAcceptsCapability(t *testing.T) {
	eco := helperEcosystem(t, `{"built":true}`)
	eco.info.Capabilities = []string{MethodArtifacts, CapabilityArtifactsProperties}
	resp, err := eco.Artifacts(context.Background(), ArtifactsRequest{Properties: map[string]string{"PackageId": "You.Lib"}})
	if err != nil || !resp.Built {
		t.Fatalf("capable plugin: %+v, %v", resp, err)
	}
}

func TestArtifactsPropertiesSubprocessLegacyRequest(t *testing.T) {
	eco := helperEcosystem(t, `{"built":true}`)
	eco.info.Capabilities = []string{MethodArtifacts}
	resp, err := eco.Artifacts(context.Background(), ArtifactsRequest{})
	if err != nil || !resp.Built {
		t.Fatalf("property-free request to old plugin: %+v, %v", resp, err)
	}
}

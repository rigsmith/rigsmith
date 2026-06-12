package redact

import (
	"encoding/json"
	"reflect"
	"testing"
)

func unmarshal(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMerge_KeepsLocalSecret(t *testing.T) {
	synced := []byte(`{"apiKey":"__CLAUDERIG_REDACTED__","effortLevel":"high"}`)
	local := []byte(`{"apiKey":"sk-ant-real-local-secret","effortLevel":"low"}`)
	out, err := MergeBytes(synced, local)
	if err != nil {
		t.Fatal(err)
	}
	m := unmarshal(t, out)
	if m["apiKey"] != "sk-ant-real-local-secret" {
		t.Errorf("local secret not preserved: %v", m["apiKey"])
	}
	if m["effortLevel"] != "high" { // synced non-secret wins
		t.Errorf("synced value should win: %v", m["effortLevel"])
	}
}

func TestMerge_FreshMachineDropsPlaceholder(t *testing.T) {
	synced := []byte(`{"apiKey":"__CLAUDERIG_REDACTED__","effortLevel":"high"}`)
	out, err := MergeBytes(synced, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := unmarshal(t, out)
	if _, present := m["apiKey"]; present {
		t.Errorf("placeholder should be dropped on fresh machine, got %v", m["apiKey"])
	}
	if m["effortLevel"] != "high" {
		t.Errorf("non-secret should restore: %v", m["effortLevel"])
	}
}

func TestMerge_PreservesLocalOnlyKeys(t *testing.T) {
	synced := []byte(`{"shared":"x"}`)
	local := []byte(`{"shared":"old","localOnly":"keep me"}`)
	out, _ := MergeBytes(synced, local)
	m := unmarshal(t, out)
	want := map[string]any{"shared": "x", "localOnly": "keep me"}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

func TestMerge_NestedContainerSecret(t *testing.T) {
	synced := []byte(`{"env":{"API":"__CLAUDERIG_REDACTED__","PORT":"3000"}}`)
	local := []byte(`{"env":{"API":"real-key","PORT":"9999"}}`)
	out, _ := MergeBytes(synced, local)
	m := unmarshal(t, out)
	env := m["env"].(map[string]any)
	if env["API"] != "real-key" {
		t.Errorf("nested secret not kept: %v", env["API"])
	}
	if env["PORT"] != "3000" { // synced non-secret wins even nested
		t.Errorf("nested synced value should win: %v", env["PORT"])
	}
}

package commands

import (
	"encoding/json"
	"testing"

	"github.com/rigsmith/rigsmith/internal/clauderig/config"
	"github.com/spf13/cobra"
)

func TestChunkStorageConfig(t *testing.T) {
	cfg := config.Default()
	if cfg.ChunkTranscripts == nil || !*cfg.ChunkTranscripts {
		t.Fatal("new configurations must default to on")
	}
	var existing config.Config
	if err := json.Unmarshal([]byte(`{"schema":1}`), &existing); err != nil {
		t.Fatal(err)
	}
	if existing.ChunkTranscripts != nil {
		t.Fatal("an omitted key must remain auto")
	}
	if got, _ := configValue(&existing, "chunkTranscripts"); got != "auto" {
		t.Fatalf("omitted key reports %q, want auto", got)
	}

	for _, value := range []string{"true", "false"} {
		if _, err := applyConfigSet(&cobra.Command{}, cfg, "chunkTranscripts", value); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var round config.Config
		if err := json.Unmarshal(b, &round); err != nil {
			t.Fatal(err)
		}
		if round.ChunkTranscripts == nil || *round.ChunkTranscripts != (value == "true") {
			t.Fatal("explicit false lost during serialization")
		}
	}
	if _, err := applyConfigSet(&cobra.Command{}, cfg, "chunkTranscripts", "auto"); err != nil || cfg.ChunkTranscripts != nil {
		t.Fatal("auto must clear override")
	}
	if _, err := applyConfigSet(&cobra.Command{}, cfg, "chunkTranscripts", "invalid"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if _, err := applyConfigSet(&cobra.Command{}, cfg, "redactTranscripts", "true"); err != nil || !cfg.RedactTranscripts {
		t.Fatal("cannot enable transcript scrubbing")
	}
}

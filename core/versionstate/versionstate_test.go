package versionstate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadWrite(t *testing.T) {
	dir := t.TempDir()
	s, err := Read(dir)
	if err != nil || s == nil || len(s.Packages) != 0 {
		t.Fatalf("absent file: %+v, %v", s, err)
	}
	s.Set("Mermaider", "0.12.2")
	s.Set("Acme.Lib", "1.0.0")
	if err := Write(dir, s); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	want := "{\n  \"packages\": {\n    \"Acme.Lib\": \"1.0.0\",\n    \"Mermaider\": \"0.12.2\"\n  }\n}\n"
	if string(data) != want {
		t.Fatalf("file:\n%s\nwant:\n%s", data, want)
	}
	again, err := Read(dir)
	if err != nil || again.Get("Mermaider") != "0.12.2" || again.Get("nope") != "" {
		t.Fatalf("read back: %+v, %v", again, err)
	}
	if names := again.Names(); len(names) != 2 || names[0] != "Acme.Lib" {
		t.Fatalf("Names = %v", names)
	}
	again.Delete("Acme.Lib")
	again.Delete("never-there")
	if names := again.Names(); len(names) != 1 || names[0] != "Mermaider" {
		t.Fatalf("after Delete: %v", names)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(dir); err == nil {
		t.Fatal("a broken file read as empty")
	}
}

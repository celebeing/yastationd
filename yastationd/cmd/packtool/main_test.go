package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestZipDirSortsPathsAndExcludesDotFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"z.txt":         "z",
		"nested/a.txt":  "a",
		".ignored":      "no",
		".hidden/x.txt": "no",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := zipDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range zr.File {
		names = append(names, file.Name)
	}
	if want := []string{"nested/a.txt", "z.txt"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("archive names = %v, want %v", names, want)
	}
}

func TestReadManifestUsesIDAsDefaultName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pack.json")
	if err := os.WriteFile(path, []byte(`{"id":"sunrise","version":"1.2.3","displays":["seg7","matrix"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fromDir, err := readManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	fromFile, err := readManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromDir, fromFile) || fromDir.Name != "sunrise" || fromDir.field("displays") != "seg7,matrix" {
		t.Fatalf("manifest = %+v", fromDir)
	}
}

func TestReadManifestReportsItsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pack.json")
	if err := os.WriteFile(path, []byte(`{broken`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readManifest(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v", err)
	}
}

func TestPrintIndexSortsPacksAndBuildsReleaseURLs(t *testing.T) {
	root := t.TempDir()
	writeManifest := func(id, version, name string, displays []string) string {
		t.Helper()
		dir := filepath.Join(root, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(manifest{ID: id, Version: version, Name: name, Displays: displays})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	zulu := writeManifest("zulu", "2.0.0", "Zulu", []string{"matrix"})
	alpha := writeManifest("alpha", "1.4.0", "Alpha", []string{"seg7", "matrix"})

	oldStdout := os.Stdout
	readOut, writeOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writeOut
	printIndex("https://example.invalid/releases", []string{zulu, alpha})
	writeOut.Close()
	os.Stdout = oldStdout
	data, err := io.ReadAll(readOut)
	if err != nil {
		t.Fatal(err)
	}
	readOut.Close()

	var got struct {
		Packs []struct {
			ID       string   `json:"id"`
			Name     string   `json:"name"`
			Version  string   `json:"version"`
			Displays []string `json:"displays"`
			Tag      string   `json:"tag"`
			Asset    string   `json:"asset"`
			URL      string   `json:"url"`
		} `json:"packs"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Packs) != 2 {
		t.Fatalf("packs = %+v", got.Packs)
	}
	if got.Packs[0].ID != "alpha" || got.Packs[1].ID != "zulu" {
		t.Fatalf("pack order = %q, %q", got.Packs[0].ID, got.Packs[1].ID)
	}
	want := struct {
		ID       string
		Name     string
		Version  string
		Displays []string
		Tag      string
		Asset    string
		URL      string
	}{"alpha", "Alpha", "1.4.0", []string{"seg7", "matrix"}, "alpha-v1.4.0", "alpha-1.4.0.zip", "https://example.invalid/releases/alpha-v1.4.0/alpha-1.4.0.zip"}
	gotAlpha := got.Packs[0]
	if gotAlpha.ID != want.ID || gotAlpha.Name != want.Name || gotAlpha.Version != want.Version ||
		!reflect.DeepEqual(gotAlpha.Displays, want.Displays) || gotAlpha.Tag != want.Tag ||
		gotAlpha.Asset != want.Asset || gotAlpha.URL != want.URL {
		t.Fatalf("alpha entry = %+v, want %+v", gotAlpha, want)
	}
}

func TestPacktoolRejectsUnknownManifestField(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"id":"test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", ".", "-get", "unknown", dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("packtool accepted an unknown manifest field")
	}
	if !strings.Contains(stderr.String(), `no field "unknown"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

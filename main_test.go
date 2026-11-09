package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sorenisanerd/gotty/server"
)

func TestApplyConfigFile(t *testing.T) {
	for _, test := range []struct {
		name    string
		file    string
		content string
		dir     string
		custom  string
		port    string
		wantErr bool
	}{
		{name: "no default config", port: "8080"},
		{name: "legacy config file", file: ".gotty", content: `port = "2222"`, port: "2222"},
		{name: "Hub state directory without config", dir: ".gotty", port: "8080"},
		{name: "Hub state file without config", file: ".gotty/hub.json", content: `{"url":"http://hub.example"}`, port: "8080"},
		{name: "config alongside Hub state", file: ".gotty/config", content: `port = "2222"`, port: "2222"},
		{name: "invalid directory config", file: ".gotty/config", content: "{", wantErr: true},
		{name: "config is itself a directory", dir: ".gotty/config", wantErr: true},
		{name: "explicit config", file: "custom.hcl", content: `port = "3333"`, custom: "custom.hcl", port: "3333"},
		{name: "missing explicit config", custom: "missing.hcl", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			taskHome := t.TempDir()
			t.Setenv("HOME", taskHome)
			if test.dir != "" {
				if err := os.MkdirAll(filepath.Join(taskHome, test.dir), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if test.file != "" {
				path := filepath.Join(taskHome, test.file)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(test.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			path := "~/.gotty"
			if test.custom != "" {
				path = filepath.Join(taskHome, test.custom)
			}
			options := &server.Options{Port: "8080"}
			err := applyConfigFile(path, options)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, test.wantErr)
			}
			if !test.wantErr && options.Port != test.port {
				t.Fatalf("port = %s, want %s", options.Port, test.port)
			}
		})
	}
}

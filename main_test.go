package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sorenisanerd/gotty/server"
	"github.com/sorenisanerd/gotty/utils"
	cli "github.com/urfave/cli/v2"
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

func TestHubDisabledOption(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		env     string
		config  string
		want    bool
		wantErr bool
	}{
		{name: "enabled by default"},
		{name: "disable flag", args: []string{"--hub-disabled"}, want: true},
		{name: "environment disables Hub", env: "true", want: true},
		{name: "config disables Hub", config: "hub_disabled = true", want: true},
		{name: "flag overrides config", args: []string{"--hub-disabled=false"}, config: "hub_disabled = true"},
		{name: "flag overrides environment", args: []string{"--hub-disabled=false"}, env: "true"},
		{name: "environment overrides config", env: "true", config: "hub_disabled = false", want: true},
		{name: "old enable flag removed", args: []string{"--hub-enabled"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GOTTY_HUB_DISABLED", test.env)
			if test.env == "" {
				if err := os.Unsetenv("GOTTY_HUB_DISABLED"); err != nil {
					t.Fatal(err)
				}
			}
			options := &server.Options{}
			if err := utils.ApplyDefaultValues(options); err != nil {
				t.Fatal(err)
			}
			flags, mappings, err := utils.GenerateFlags(options)
			if err != nil {
				t.Fatal(err)
			}
			app := &cli.App{Flags: flags, Writer: io.Discard, ErrWriter: io.Discard}
			app.Action = func(c *cli.Context) error {
				if test.config != "" {
					path := filepath.Join(t.TempDir(), "config")
					if err := os.WriteFile(path, []byte(test.config), 0600); err != nil {
						return err
					}
					if err := applyConfigFile(path, options); err != nil {
						return err
					}
				}
				utils.ApplyFlags(flags, mappings, c, options)
				return nil
			}
			err = app.Run(append(append([]string{"gotty"}, test.args...), "bash"))
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, test.wantErr)
			}
			if !test.wantErr && options.HubDisabled != test.want {
				t.Fatalf("HubDisabled = %t, want %t", options.HubDisabled, test.want)
			}
		})
	}
}

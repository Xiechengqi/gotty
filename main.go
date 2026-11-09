package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	cli "github.com/urfave/cli/v2"

	"github.com/sorenisanerd/gotty/backend/localcommand"
	"github.com/sorenisanerd/gotty/pkg/homedir"
	"github.com/sorenisanerd/gotty/server"
	"github.com/sorenisanerd/gotty/utils"
)

func main() {
	app := cli.NewApp()
	app.Name = "gotty"
	app.Version = Version
	app.Usage = "Share your terminal as a web application"
	app.HideHelpCommand = true
	appOptions := &server.Options{}

	if err := utils.ApplyDefaultValues(appOptions); err != nil {
		exit(err, 1)
	}
	backendOptions := &localcommand.Options{}
	if err := utils.ApplyDefaultValues(backendOptions); err != nil {
		exit(err, 1)
	}

	cliFlags, flagMappings, err := utils.GenerateFlags(appOptions, backendOptions)
	if err != nil {
		exit(err, 3)
	}

	app.Flags = append(
		cliFlags,
		&cli.StringFlag{
			Name:    "config",
			Value:   "~/.gotty",
			Usage:   "Config file path (~/.gotty/config when ~/.gotty is a directory)",
			EnvVars: []string{"GOTTY_CONFIG"},
		},
	)

	app.Action = func(c *cli.Context) error {
		if c.NArg() == 0 {
			msg := "Error: No command given."
			cli.ShowAppHelp(c)
			exit(fmt.Errorf("%s", msg), 1)
		}

		if err := applyConfigFile(c.String("config"), appOptions, backendOptions); err != nil {
			exit(err, 2)
		}

		utils.ApplyFlags(cliFlags, flagMappings, c, appOptions, backendOptions)

		if appOptions.Quiet {
			log.SetFlags(0)
			log.SetOutput(io.Discard)
		}

		if c.IsSet("credential") {
			appOptions.EnableBasicAuth = true
		}
		if c.IsSet("tls-ca-crt") {
			appOptions.EnableTLSClientAuth = true
		}

		err = appOptions.Validate()
		if err != nil {
			exit(err, 6)
		}

		args := c.Args()
		factory, err := localcommand.NewFactory(args.First(), args.Tail(), backendOptions)
		if err != nil {
			exit(err, 3)
		}

		hostname, _ := os.Hostname()
		appOptions.TitleVariables = map[string]interface{}{
			"command":  args.First(),
			"argv":     args.Tail(),
			"hostname": hostname,
		}

		srv, err := server.New(factory, appOptions)
		if err != nil {
			exit(err, 3)
		}

		ctx, cancel := context.WithCancel(context.Background())
		gCtx, gCancel := context.WithCancel(context.Background())

		log.Printf("GoTTY is starting with command: %s", strings.Join(args.Slice(), " "))

		errs := make(chan error, 2)
		go func() {
			errs <- srv.Run(ctx, server.WithGracefullContext(gCtx))
		}()

		err = waitSignals(errs, cancel, gCancel)

		if plan, ok := srv.PendingSelfRestart(); ok {
			if err != nil && err != context.Canceled {
				fmt.Printf("Error: %s\n", err)
				exit(err, 8)
			}
			if err := server.ExecSelfRestart(plan); err != nil {
				fmt.Printf("Error: failed to exec restart: %s\n", err)
				exit(err, 8)
			}
			return nil
		}

		if err != nil && err != context.Canceled {
			fmt.Printf("Error: %s\n", err)
			exit(err, 8)
		}

		return nil
	}
	app.Run(os.Args)
}

func applyConfigFile(path string, options ...interface{}) error {
	if path == "~/.gotty" {
		info, err := os.Stat(homedir.Expand(path))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.IsDir() {
			path = filepath.Join(path, "config")
			if _, err := os.Stat(homedir.Expand(path)); os.IsNotExist(err) {
				return nil
			}
		}
	}
	return utils.ApplyConfigFile(path, options...)
}

func exit(err error, code int) {
	if err != nil {
		fmt.Println(err)
	}
	os.Exit(code)
}

func waitSignals(errs chan error, cancel context.CancelFunc, gracefullCancel context.CancelFunc) error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(
		sigChan,
		syscall.SIGINT,
		syscall.SIGTERM,
	)

	select {
	case err := <-errs:
		return err

	case s := <-sigChan:
		switch s {
		case syscall.SIGINT:
			gracefullCancel()
			fmt.Println("C-C to force close")
			select {
			case err := <-errs:
				return err
			case <-sigChan:
				fmt.Println("Force closing...")
				cancel()
				return <-errs
			}
		default:
			cancel()
			return <-errs
		}
	}
}

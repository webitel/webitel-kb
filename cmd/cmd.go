package cmd

import (
	"os"

	"github.com/urfave/cli/v2"
	"github.com/webitel/webitel-kb/cmd/migrate"
	"github.com/webitel/webitel-kb/cmd/server"
	"github.com/webitel/webitel-kb/internal/model"
)

func Run() error {
	app := &cli.App{
		Name:  model.ServiceName,
		Usage: "Microservice for Webitel platform",
		Commands: []*cli.Command{
			server.CMD(),
			migrate.CMD(),
		},
	}

	return app.Run(os.Args)
}

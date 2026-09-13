package cmd

import (
	"os"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/dgethings/chunt/internal/server"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the language server",
	RunE: func(cmd *cobra.Command, args []string) error {
		srv := server.New(Version)
		srv.RegisterFeature(cisco_ios_jinja2.New())

		assigner, err := srv.Assigner()
		if err != nil {
			return err
		}

		opts := &jrpc2.ServerOptions{
			AllowPush:   true,
			Concurrency: 1,
		}

		ioChannel := channel.Header("")(os.Stdin, os.Stdout)
		jrpcSrv := jrpc2.NewServer(assigner, opts)
		jrpcSrv.Start(ioChannel)
		jrpcSrv.Wait()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

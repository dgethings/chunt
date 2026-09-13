package cmd

import (
	"fmt"

	"github.com/dgethings/chunt/internal/features/cisco_ios_jinja2"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(Version)
		if v := cisco_ios_jinja2.GrammarVersion(); v != "" {
			fmt.Printf("grammar: %s %s\n", cisco_ios_jinja2.GrammarModule, v)
		}
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/codeen15/pyro/pkg/engine/docs"
)

func docsCmd() *cobra.Command {
	var wsDir, out string
	cmd := &cobra.Command{
		Use:   "docs <collection>",
		Short: "Generate an HTML API reference from a collection",
		Long: "Writes one self-contained HTML page: every request with its description (Markdown),\n" +
			"parameters, body, saved examples and a cURL command. Secret values and credentials are\n" +
			"left out; variables appear as {{name}}.",
		Example: "  pyro docs shop -o shop-api.html",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ws, err := openWorkspace(wsDir)
			if err != nil {
				return err
			}
			b, err := ws.Snapshot(args[0])
			if err != nil {
				return err
			}
			page, err := docs.Render(b)
			if err != nil {
				return err
			}
			if out == "" || out == "-" {
				_, err = os.Stdout.Write(page)
				return err
			}
			if err := os.WriteFile(out, page, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Wrote %s (%d requests)\n", out, len(b.Requests))
			return nil
		},
	}
	cmd.Flags().StringVarP(&wsDir, "workspace", "w", "", "workspace directory (same default as serve)")
	cmd.Flags().StringVarP(&out, "output", "o", "", "file to write (default: standard output)")
	return cmd
}

package cli

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

// group builds a parent command with no action of its own.
func group(use, short string, subs ...*cobra.Command) *cobra.Command {
	g := &cobra.Command{Use: use, Short: short}
	g.AddCommand(subs...)
	return g
}

// listCmd builds a "list" subcommand that GETs a collection path, with
// optional --page / --per-page pagination.
func listCmd(path, short string) *cobra.Command {
	var page, perPage int
	c := &cobra.Command{
		Use:   "list",
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := path
			q := url.Values{}
			if cmd.Flags().Changed("page") {
				q.Set("page", strconv.Itoa(page))
			}
			if cmd.Flags().Changed("per-page") {
				q.Set("per_page", strconv.Itoa(perPage))
			}
			if len(q) > 0 {
				p += "?" + q.Encode()
			}
			return runRender(cmd, "GET", p, nil)
		},
	}
	c.Flags().IntVar(&page, "page", 1, "page number")
	c.Flags().IntVar(&perPage, "per-page", 25, "results per page")
	return c
}

// getCmd builds a "get <id>" subcommand that GETs path/<id>.
func getCmd(path, short string) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "GET", path+"/"+args[0], nil)
		},
	}
}

// deleteCmd builds a "<use> <id>" subcommand that DELETEs a formatted path.
func deleteCmd(use, short, pathFmt string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "DELETE", fmt.Sprintf(pathFmt, args[0]), nil)
		},
	}
}

// subGetCmd builds a "<verb> <id>" subcommand GETting a formatted sub-path.
func subGetCmd(use, short, pathFmt string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, "GET", fmt.Sprintf(pathFmt, args[0]), nil)
		},
	}
}

// watchCmd builds a "watch <id>" subcommand that streams an SSE endpoint.
func watchCmd(short, pathFmt string) *cobra.Command {
	return &cobra.Command{
		Use:   "watch <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			return c.Stream(cmd.Context(), fmt.Sprintf(pathFmt, args[0]), func(line string) {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			})
		},
	}
}

// actionCmd builds a "<use> <id>" subcommand issuing a no-body request.
func actionCmd(use, short, method, pathFmt string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRender(cmd, method, fmt.Sprintf(pathFmt, args[0]), nil)
		},
	}
}
